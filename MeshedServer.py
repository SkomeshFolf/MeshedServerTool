import re
import subprocess
import time
import pygtail
import threading
import requests
import json
import os
import platform
import configparser
import psutil
import ast
from datetime import datetime, time, timedelta
import time
import shutil
import socket
import traceback
import hashlib
import platformdirs
import chardet
from enum import Enum
import logging

class OSErrorDetectionError (Exception):
    def __init__ (self, message="Either unable to detect the current OS or current OS is not supported."):
        self.message = message
        super().__init__(self.message)

class LogLevel (Enum):
    DEBUG = 10
    INFO = 20
    WARNING = 30
    ERROR = 40
    CRITICAL = 50

class Server:
    def __init__(self, name, config, server_info):
        self.name = name
        self.server_info = server_info
        self.config_path = config

        self.server_process = None
        self.log = None
        self.analysis_thread = None
        self.server_started = False

        self.current_line = 0
        self.log_check_interval = int (read_global_config()['General']['log_checking_interval'])
        self.last_crash = None
        self.manual_kill_flag = False
        self.manual_shutdown_flag = False

        self.lock = threading.Lock()

    def create_server (self, shared_dir=False):
        self.server_info.server_status_change (-5)
        register_server_creating (self.name)
        self.read_server_config ()
        self.launch_server_dry (shared_dir)
        time.sleep (3)
        self.kill_server ()

    def init_server (self):
        self.read_server_config ()

        if not self.check_if_valid_install_dir():
            self.wait_for_valid_install_dir()

        UserReport.register_reports_directory (os.path.join (self.saved_file_path, 'Reports'))
    
    def read_server_config (self):
        self.config = read_config (self.config_path)
        self.update_config_settings()
    
    def update_config_settings (self):
        self.name = self.config['General']['server_name']
        self.install_dir = self.config['General']['install_dir']
        self.shared_dir = ast.literal_eval (self.config['General']['shared_install_dir'])
        self.max_reloads = int(self.config['General']['max_reloads'])
        self.starting_gamemode = self.config['General']['starting_gamemode']
        self.restricted_gamemode = self.config['General']['restricted_gamemode']
        self.port = int(self.config['General']['port'])
        self.query_port = int(self.config['General']['queryport'])
        self.server_args = self.config['General']['server_args']

        self.update_file_paths()

        self.update_config_saved_path ()

        if (self.config['General']['active_hours'] == ''):
            self.active_hours = False
        else:
            timeSplit = self.config['General']['active_hours'].split("-")
            self.active_hours = True
            self.start_time = datetime.strptime (timeSplit[0], '%H:%M').time()
            self.end_time = datetime.strptime (timeSplit[1], '%H:%M').time()

    def update_server_path_name (self, new_name):
        global data_dir

        self.name = new_name
        self.server_info.name = new_name
        self.config_path = os.path.join (data_dir, f"Server_{new_name}", "config.ini")
        self.read_server_config ()
        
    def update_file_paths (self):
        os_name = platform.system ()
        if os_name == "Windows":
            if self.shared_dir:
                self.log_file_path = os.path.join(self.install_dir, 'WindowsServer', 'Pandemic', 'Saved', 'Logs', self.name, f'{self.name}.log')
            else:
                self.log_file_path = os.path.join(self.install_dir, 'WindowsServer', 'Pandemic', 'Saved', 'Logs', 'Pandemic.log')
            self.saved_file_path = os.path.join(self.install_dir, 'WindowsServer', 'Pandemic', 'Saved')
            self.server_executable = os.path.join(self.install_dir, 'WindowsServer', 'PandemicServer.exe')
        elif os_name == "Linux":
            if self.shared_dir:
                self.log_file_path = os.path.join(self.install_dir, 'LinuxServer', 'Pandemic', 'Saved', 'Logs', self.name, f'{self.name}.log')
            else:
                self.log_file_path = os.path.join(self.install_dir, 'LinuxServer', 'Pandemic', 'Saved', 'Logs', 'Pandemic.log')
            self.saved_file_path = os.path.join(self.install_dir, 'LinuxServer', 'Pandemic', 'Saved')
            self.server_executable = os.path.join(self.install_dir, 'LinuxServer', 'Pandemic', 'Binaries', 'Linux', 'PandemicServer')
        else:
            raise OSErrorDetectionError
    
    def update_config_saved_path (self):
        config = configparser.ConfigParser()
        config.read (self.config_path)
        config.set ('General', 'saved_path_dont_touch', self.saved_file_path)
        with open (self.config_path, 'w') as configfile:
            config.write (configfile)

    def check_if_valid_install_dir (self):
        if os.path.exists (self.install_dir):
            if os.path.exists (os.path.join (self.install_dir, "WindowsServer")) or os.path.exists (os.path.join (self.install_dir, "LinuxServer")):
                return True
            else:
                return False
        else:
            return False
    
    def wait_for_valid_install_dir (self):
        path_valid = False

        while not path_valid:
            self.read_server_config()
            path_valid = self.check_if_valid_install_dir()

            if path_valid:
                break

            write_to_log_error ("Could not find server at specified directory. Please update the directory.", LogLevel.ERROR, self.name)

            time.sleep (10)

    def start_log_analysis (self):
        thread = threading.Thread (target=self.analyze_log, daemon=True)
        thread.start()

    def start_server (self):
        self.server_info.server_status_change (2)
        register_server_start (self.name)
        self.read_server_config()
        update_server_path_name (self.name) 
        self.init_motd ()
        self.reset_vars()
        self.launch_server()
        
    def launch_server (self):
        if not self.server_process:
            if self.shared_dir:
                server_config = os.path.join (self.saved_file_path, "Config", f"{self.name}.ini")
                if not os.path.exists (server_config):
                    shutil.copy (os.path.join (self.saved_file_path, "Config", "ServerConfig.ini"), os.path.join (self.saved_file_path, "Config", f"{self.name}.ini"))
            else:
                server_config = os.path.join (self.saved_file_path, "Config", "ServerConfig.ini")
            
            config = configparser.ConfigParser ()
            config.read (server_config)

            server_name = config['/Game/SCPPandemic/Blueprints/GI_PandemicGameInstance.GI_PandemicGameInstance_C']['servername']

            essential_server_args = [
                self.starting_gamemode,
                '-log',
                f"-port={self.port}",
                f"-queryport={self.query_port}",
                f"-SteamServerName={server_name}"
            ]

            if self.shared_dir:
                essential_server_args.append (f"-Log={self.name}/{self.name}.log")
                essential_server_args.append (f"-ConfigFileName={self.name}.ini")

            server_args_raw = self.server_args
            server_args = essential_server_args + server_args_raw.split(',')
            command = [self.server_executable] + server_args
            self.server_process = subprocess.Popen(command)
            self.server_info.server_restarts = self.server_info.server_restarts + 1
            self.start_log_analysis()

    def launch_server_dry (self, shared_dir):
        server_args_raw = []
        if shared_dir:
            server_args_raw = [
                "-log",
                f"-Log={self.name}/{self.name}.log",
                f"-ConfigFileName={self.name}.ini"
            ]
        else:
            server_args_raw = [
                "-log"
            ]
        server_args = server_args_raw
        command = [self.server_executable] + server_args
        self.server_process = subprocess.Popen(command)

    def execute_server_start (self):
        self.manual_kill_flag = False
        self.manual_shutdown_flag = False
        self.start_server()

    def execute_server_restart (self):
        self.restart_server ("Manual Restart")

    def execute_server_stop (self):
        self.manual_shutdown_flag = True

    def execute_server_kill (self):
        self.manual_kill_flag = True
        self.shutdown_server ()

    def wake_server (self):
        self.server_info.server_status_change (1)
        register_server_wake (self.name)
        time.sleep (3)
        self.init_server()

    def active_server (self):
        if self.server_info.server_status != 5:
            self.server_info.server_status_change (5)
            register_server_active (self.name)

    def stop_server(self):
        self.server_info.server_status_change (0)
        register_server_stop (self.name)
        time.sleep (2)
        if self.server_process:
            self.kill_server()
    
    def shutdown_server(self):
        self.stop_server()
        self.reset_vars()
        self.server_info.server_status_change (-3)
      
    def kill_server (self):
        if self.server_process:
            try:
                parent_pid = self.server_process.pid
                parent = psutil.Process(parent_pid)
                children = parent.children(recursive=True)
                for child in children:
                    child.terminate()
                psutil.wait_procs(children, timeout=10)
                parent.terminate()
                parent.wait(timeout=10)
                self.server_info.server_status_change (-3)
                register_server_offline (self.name)
            except psutil.NoSuchProcess:
                pass
            finally:
                self.server_process = None

    def suspend_server(self):
        self.shutdown_server()
        self.server_info.server_status_change (-1)
        register_server_suspend (self.name)

        while True:
            if self.is_active_hours (self.start_time, self.end_time):
                break

            time.sleep (60)
        
        self.wake_server()

    def restart_server(self, reason):
        self.server_info.server_status_change (3)
        register_server_restart (self.name, reason)
        if not reason:
            self.init_motd()

        time.sleep (0.5)
        self.stop_server()
        time.sleep (1)
        self.start_server()
        
    def idle_server (self):
        self.server_info.server_status_change (4)
        register_server_idle (self.name)

    def server_crashed (self):
        self.last_crash = datetime.now().time()
        self.server_info.server_status_change (-2)
        time.sleep (3)
        self.restart_server ("Server crash")

    def reset_vars(self):
        self.log = None
        self.server_info.reset_variables()
        self.current_line = 0
        self.server_started = False
        self.last_crash = None

    def init_motd (self):
        path = self.saved_file_path
        config = self.config
        global_motd = read_global_config()['MOTD']['global_server_motd']
        motd = self.config['MOTD']['motd']
        join_motd = self.config ['MOTD']['join_motd']
        crash_motd = ast.literal_eval (config ['MOTD']['crash_motd'])
        
        with open(f"{path}/Messages.ini", 'w') as message_file:
            if (motd and motd != ''):
                if crash_motd != None and self.last_crash != None:
                    message_file.write(f"{global_motd}/{motd.strip()}/The last server crashed was at: {self.last_crash.strftime('%H:%M')} PST. Lets hope it doesn't crash again!,0,00:05:00\n")
                else:
                    message_file.write(f"{global_motd}/{motd.strip()},0,00:07:30\n")
            if join_motd and join_motd != '':
                message_file.write (f"{join_motd},2,00:00:07\n")
            if self.active_hours:
                end_time = datetime.combine (datetime.today(), self.end_time)

                message_file.write (f"The server will be shutdown in one hour., 1, {(end_time - timedelta(hours=1)).time().strftime('%H:%M')}:00\n")
                message_file.write (f"The server will be shutdown in 30 minutes., 1, {(end_time - timedelta(minutes=30)).time().strftime('%H:%M')}:00\n")
                message_file.write (f"The server will shutdown after this game ends., 1, {end_time.time().strftime('%H:%M')}:00\n")
   
    def is_active_hours (self):
        current_time = datetime.now().time()
        if self.start_time <= current_time <= self.end_time:
            return True
        else:
            return False
        
    def is_idle_for_too_long (self):
        if self.server_info.idle_time != 0:
            if self.server_info.idle_time + timedelta(minutes=2) < datetime.now():
                self.restart_server ("Server idle for 2 minutes.")
                return True
            
        return False


    def analyze_log(self):
        # If this starts when we are beyond the active hours, if it is, suspend.
        if self.active_hours:
            if not self.is_active_hours ():
                self.suspend_server()
                return
        
        with self.lock:
            # Keep analyzing the log until it should stop.
            # This will continue until the server suspends.
            server_logging = True

            while server_logging and not self.manual_kill_flag:
                time.sleep(3)

                # Setup the log file for reading
                try:
                    self.log = pygtail.Pygtail(self.log_file_path)
                except FileNotFoundError:
                    write_to_log_error (f"Log file {self.log_file_path} not found.", LogLevel.ERROR, self.name)
                    return
                except Exception as e:
                    write_to_log_error (f"Unexpected exception while opening log. {e}", LogLevel.ERROR, self.name)

                
                # Keep executing while the server is active.
                # Server will be labeled inactive if the server restarts, suspends or crashes.
                server_active = True
                while server_active and not self.manual_kill_flag:

                    # Check if the server has crashed
                    if self.server_process != None:
                        if self.server_process.poll() != None:
                            self.server_crashed()
                            server_active = False
                            break
                    else:
                        self.server_crashed()

                    # Check if idle for more than 2 minutes, restart the server.
                    if self.is_idle_for_too_long ():
                        break

                    # Go through each new line 
                    with open(self.log_file_path, 'r') as log_file:
                        for _ in range(self.current_line):
                            log_file.readline()

                        for line in log_file:
                            self.current_line += 1

                            # Has an objective been completed?
                            objective_completed = log_is_objective_completed (line)
                            if objective_completed:
                                self.server_info.objective_completed (objective_completed)

                            # Has a checkpoint been reached? 
                            checkpoint = log_is_new_checkpoint (line)
                            if checkpoint:
                                self.server_info.new_checkpoint (checkpoint)

                            # Has a player died?
                            player_death = log_has_player_died (line)
                            if player_death:
                                self.server_info.player_died ()

                            # Has the game ended?
                            game_ended = log_has_game_ended (line)
                            if game_ended:
                                self.server_info.game_ended ()

                            # Has the game started?
                            game_started = log_is_game_started (line)
                            if game_started:
                                self.server_info.game_started ()

                            # Is the next game declared?
                            # next_game = log_is_next_game (line)

                            # Is the next game loading?
                            game_loading = log_is_game_loading (line)
                            if game_loading:
                                self.server_info.game_loading (game_loading)
                                
                                if self.manual_shutdown_flag:
                                    self.shutdown_server()
                                    server_active = False
                                    server_logging = False
                                    break

                                if self.active_hours:
                                    if not self.is_active_hours ():
                                        self.suspend_server()
                                        server_active = False
                                        server_logging = False
                                        break

                                if self.server_info.gamemode_changes > self.max_reloads:
                                    self.restart_server(f"Server reloaded {self.server_info.gamemode_changes} times")
                                    server_active = False
                                    break
                                elif self.restricted_gamemode != '':
                                    delimited_string = self.restricted_gamemode.split('?')
                                    if len (delimited_string) == 1:
                                        if self.server_info.current_game != delimited_string[0]:
                                            self.restart_server(f"Server loaded a gamemode that is not {self.restricted_gamemode}")
                                            server_active = False
                                            break

                            # Is a new gamemode?
                            gamemode = log_is_new_gamemode (line)
                            if gamemode:
                                self.server_info.new_gamemode (gamemode)

                                delimited_string = self.restricted_gamemode.split('?')
                                if len (delimited_string) > 1:
                                    if self.server_info.current_game != delimited_string[0] or self.server_info.current_gamemode != delimited_string[1]:
                                        self.restart_server(f"Server loaded a gamemode that is not {self.restricted_gamemode}")
                                        server_active = False
                                        break

                            # Has session been created?
                            session_create = log_is_session_creation (line)
                            if session_create:
                                self.server_info.session_created ()

                            # Is server idling?
                            server_idle = log_is_entering_idle (line)
                            if server_idle:
                                self.idle_server()

                            # Is the latest log a player joining? Log it in the server info.
                            player_name, player_hex = log_is_player_joined(line)
                            if player_hex:
                                player_id = log_get_steam_id_from_hex (player_hex)
                                if player_id not in self.server_info.current_users:
                                    self.server_info.player_join(player_id, player_name)

                            # Is the latest log a player leaving? Log it in the server info.
                            player_id = log_is_player_leave(line)
                            if player_id:
                                if player_id in self.server_info.current_users:
                                    self.server_info.player_leave(player_id)
  
                            # Is this the first time the server has started? Init the server.
                            if not self.server_started:
                                session_create = log_is_session_creation (line)

                                if session_create:
                                    self.server_info.server_status_change (4)
                                    self.idle_server()
                                    self.server_started = True
                                
                    send_server_info ()
                    time.sleep(self.log_check_interval)

class ServerInfo:
    def __init__ (self, name):
        self.name = name
        self.server_restarts = 0
        self.server_status = 'Offline'
        self.reset_variables()
    
    def player_join (self, player, player_name):
        self.total_user_joins += 1
        self.joined_users.add(player)
        self.current_users[str(player)] = player_name
        self.idle_time = 0
        register_player_join (self.name, player, player_name)
    
    def player_leave (self, player):
        self.total_user_disconnects += 1
        self.disconnected_users.add(player)
        player_name = self.current_users[player]
        del self.current_users[player]

        register_player_leave (self.name, player, player_name)
        self.check_if_server_empty()

    def check_if_server_empty (self):
        if len (self.current_users) == 0:
            self.server_empty()

    def server_empty (self):
        self.idle_time = datetime.now()
        register_server_empty (self.name)

    def game_change (self, game):
        self.reset_game_variables()

        if game != self.current_gamemode:
            self.gamemode_changes += 1
            self.previous_game = self.current_game
            self.current_game = game
        else:
            self.game_attempts += 1
            self.current_game = game

    def new_checkpoint (self, checkpoint):
        self.current_checkpoint = checkpoint
        register_checkpoint (self.name, checkpoint)

    def objective_completed (self, objective):
        self.last_completed_objective = objective
        register_objective_completed (self.name, objective)

    def player_died (self):
        self.player_deaths += 1
        register_player_died (self.name)

    def game_ended (self):
        self.server_status_change (6)
        register_game_ended (self.name)

    def game_started (self):
        self.server_status_change (5)
        register_game_started (self.name)
    
    def game_loading (self, game):
        self.game_change (game)
        self.server_status_change (7)
        register_game_loading (self.name, game)

    def new_gamemode (self, gamemode):
        self.current_gamemode = gamemode
        register_gamemode_loading (self.name, gamemode)
    
    def session_created (self):
        self.server_status_change (4)
        register_session_created (self.name)

    def reset_game_variables (self):
        self.player_deaths = 0
        self.current_checkpoint = None
        self.last_completed_objective = None
        
    def reset_variables (self):
        self.current_game = None
        self.current_gamemode = None
        self.previous_game = None
        self.joined_users = set()
        self.disconnected_users = set()
        self.current_users = {}
        self.gamemode_changes = 0
        self.total_user_joins = 0
        self.total_user_disconnects = 0
        self.game_attempts = 0
        self.idle_time = 0
        self.reset_game_variables()

    def server_status_change (self, new_status):
        status_dict = {
            -5: 'Creating',
            -3: 'Offline',
            -2: 'Crashed',
            -1: 'Suspended',
            0: 'Stopping',
            1: 'Waking',
            2: 'Starting',
            3: 'Restarting',
            4: 'Idle',
            5: 'Active',
            6: 'Game Ended',
            7: 'Game Starting'
        }
        self.server_status = status_dict.get (new_status, 'Offline')
        send_server_info()
    
    def __repr__(self):
        return f"ServerInfo(name={self.name}, " \
               f"previous_game={self.previous_game}, " \
               f"current_game={self.current_game}, " \
               f"current_gamemode={self.current_gamemode}, " \
               f"current_checkpoint={self.current_checkpoint}, " \
               f"last_completed_objective={self.last_completed_objective}, " \
               f"player_deaths={self.player_deaths}, " \
               f"joined_users={self.joined_users}, " \
               f"disconnected_users={self.disconnected_users}, " \
               f"current_users={self.current_users}, " \
               f"gamemode_changes={self.gamemode_changes}, " \
               f"total_user_joins={self.total_user_joins}, " \
               f"total_user_disconnects={self.total_user_disconnects}, " \
               f"server_restarts={self.server_restarts}, " \
               f"server_status={self.server_status}) "
    
class UserReport:
    report_directories = []
    checking_thread = None

    def __init__ (self, target, target_id, source, source_id, date, reason, text):
        self.target = target
        self.target_id = target_id
        self.source = source
        self.source_id = source_id
        self.date = date
        self.reason = reason
        self.text = text
        self.hash = self.generate_hash()

    def generate_hash (self):
        hasher = hashlib.md5()
        hasher.update(f"{self.target}{self.target_id}{self.source}{self.source_id}{self.date}{self.reason}{self.text}".encode('utf-8'))
        return hasher.hexdigest()
    
    @staticmethod
    def register_reports_directory (dir):
        if dir not in UserReport.report_directories:
            UserReport.report_directories.append(dir)
            if not os.path.exists (dir):
                os.makedirs (dir)
                logging.info (f"{dir} doesn't exist. Creating..")

    @staticmethod
    def remove_reports_directory (dir):
        UserReport.report_directories.remove (dir)

    @staticmethod
    def start_report_checking_thread ():
        if UserReport.checking_thread is None:
            UserReport.checking_thread = threading.Thread(target=UserReport.report_checking_thread, daemon=True).start()
    
    @staticmethod
    def report_checking_thread ():
        time.sleep (5)
        while True:
            UserReport.search_directories ()
            time.sleep (45)

    @staticmethod
    def search_directories():
        new_reports = []
        for directory in UserReport.report_directories:
            if not os.path.isdir(directory):
                write_to_log_error (f"Directory '{directory}' does not exist.", LogLevel.WARNING, method="UserReport.search_directories()")
                UserReport.remove_reports_directory(directory)
                continue

            for filename in os.listdir(directory):
                file_path = os.path.join(directory, filename)
                if os.path.isfile(file_path):
                    with open(file_path, 'rb') as file:
                        raw_data = file.read()

                    result = chardet.detect(raw_data)
                    encoding = result['encoding']

                    try:
                        with open(file_path, 'r', encoding=encoding) as file:
                            file_contents = file.read()
                    except UnicodeDecodeError:
                        write_to_log_error (f"Could not decode file '{file_path}'. Attempting to read with errors='replace'.", LogLevel.WARNING, method="UserReport.search_directories()")
                        with open(file_path, 'r', encoding=encoding, errors='replace') as file:
                            file_contents = file.read()

                    report = UserReport.parse_report(file_contents)

                    if not UserReport.has_report_been_handled(report.hash):
                        new_reports.append(report)

        send_new_reports (new_reports)
                
    @staticmethod
    def has_report_been_handled (hash):
        handled_path = os.path.join (data_dir, "handled_reports.txt")

        if not os.path.isfile(handled_path):
            with open (handled_path, 'w') as file:
                pass
    
        with open(handled_path, 'r') as file:
            for line in file:
                if line.strip() == hash:
                    return True
        
        return False

    @staticmethod
    def parse_report(file_contents):
        # Split the lines of the file
        lines = file_contents.split('\n')

        # Parse individual fields based on line number
        target_id, target = lines[0].split(',')
        source_id, source = lines[1].split(',')
        date_str = lines[2].strip()
        date = date_str
        reason = lines[4].strip()
        text = lines[5].strip()

        return UserReport(target, target_id, source, source_id, date, reason, text)

    @staticmethod
    def handle_report (hash):
        handled_path = os.path.join (data_dir, "handled_reports.txt")

        with open (handled_path, 'a') as file:
            file.write (hash + '\n')

    @staticmethod 
    def delete_report (hash):
        raise NotImplementedError
    
    def __str__ (self):
        return f"Report(server={self.server}, target={self.target}, target_id={self.target_id}, source={self.source}, source_id={self.source_id}, date={self.date}, reason={self.reason}, text={self.text}, hash={self.hash})"
    
    def __eq__ (self, other):
        if isinstance(other, UserReport):
            return self.hash == other.hash
        return False

servers = []
server_info = []
main_log_file = None
is_using_web_server = False
web_server_online = False
wait_for_web_server_thread = None

web_server_address = None
web_server_port = None

def register_player_join (server, player, player_name):
    write_to_log (server, f"Player {player_name} [{player}] connected.")
    send_server_info ()

def register_player_leave (server, player, player_name):
    write_to_log (server, f"Player {player_name} [{player}] has disconnected.")
    send_server_info ()

def register_server_restart (server, reason):
    write_to_log (server, f"Server restarted for: {reason}.")
    send_server_info ()
    
def register_server_start (server):
    write_to_log (server, f"Server started.")
    send_server_info ()

def register_server_active (server):
    write_to_log (server, f"Server active.")
    send_server_info ()

def register_server_stop (server):
    write_to_log (server, f"Server stopped.")
    send_server_info ()

def register_server_offline (server):
    write_to_log (server, f"Server offline.")
    send_server_info ()

def register_server_suspend (server):
    write_to_log (server, "Server suspended.")
    send_server_info ()

def register_server_wake (server):
    write_to_log (server, "Server waking from suspension.")
    send_server_info ()

def register_server_idle (server):
    write_to_log (server, "Server is now idle.")
    send_server_info ()

def register_server_creating (server):
    write_to_log (server, "Server is being created for the first time.")
    send_server_info()

def register_server_created (server):
    write_to_log (server, "Server successfully created.")

def register_game_change (server, game):
    write_to_log (server, f"Game changed to {game}.")

def register_checkpoint (server, checkpoint):
    write_to_log (server, f"Activated checkpoint {checkpoint}.")

def register_objective_completed (server, objective):
    write_to_log (server, f"Completed objective {objective}.")

def register_player_died (server):
    write_to_log (server, f"Player died.")

def register_game_ended (server):
    write_to_log (server, "Game ended.")

def register_game_started (server):
    write_to_log (server, "Game started.")

def register_game_loading (server, game):
    write_to_log (server, f"Loading {game}.")

def register_gamemode_loading (server, gamemode):
    write_to_log (server, f"Gamemode: {gamemode}.")

def register_session_created (server):
    write_to_log (server, "Session created. Now idling.")

def register_server_empty (server):
    write_to_log (server, "Server empty.")

def log_is_objective_completed (line):
    match = re.search(r'LogObjectives: Completed Objective (.*?) successfully', line)
    return match.group(1) if match else None

def log_is_new_checkpoint (line):
    match = re.search (r'LogGameState: Unlocked Checkpoint (.*)', line)
    return match.group(1) if match else None

def log_has_player_died (line):
    match = re.search (r'LogBlueprintUserMessages: Player Died', line)
    return bool (match)

def log_has_game_ended (line):
    match = re.search (r'LogBlueprintUserMessages: Changing Game status to GS_PostGame', line)
    return bool (match)

def log_is_game_started (line):
    match = re.search (r'LogBlueprintUserMessages: Gamemode started', line)
    return bool (match)

def log_is_next_game (line):
    match = re.search(r'Map vote has concluded, travelling to (.+)', line)
    return match.group(1) if match else None

def log_is_game_loading (line):
    match = re.search (r'LogAIModule: Creating AISystem for world (.*)', line)

    if match:
        if match.group(1) == 'TransitionMap':
            return None
        else:
            return match.group (1)
    else:
        return None
    
def log_is_new_gamemode (line):
    match = re.search (r'LogLoad: Game class is \'(.*?)\'', line)
    return match.group(1) if match else None
    
def log_is_session_creation (line):
    match = re.search (r'Create session complete', line)
    return bool (match)

def log_is_entering_idle (line):
    match = re.search (r'Entering Standby, going to standby map M_ServerDefault.', line)
    return bool (match)

def log_is_player_joined (line):
    # old_match = re.search(r'Sending auth result to user (\d+)', line)
    match = re.search(r'\?Name=(.*?) userId:.*?\[?(0x[0-9A-Fa-f]+)\]', line)
    if match:
        return match.group(1), match.group (2)
    else:
        return None, None

def log_is_player_leave (line):
    close_match = re.search(r'UNetConnection::Close: \[UNetConnection\] RemoteAddr: (\d+):', line)

    if close_match:
        return close_match.group(1)
    
    kick_match = re.search(r'Successfully kicked player (\d+)', line)

    if (kick_match):
        return kick_match.group(1)
    
    cleanup = re.search(r'LogNet: UChannel::CleanUp: ChIndex == \d+. Closing connection. \[UChannel\] ChIndex: \d+, Closing: \d+ \[UNetConnection\] RemoteAddr: (\d+):', line)

    if cleanup:
        return cleanup.group(1)
    
    return None

def log_is_player_id(log_file_path):
    steam_ids = set()

    with open(log_file_path, 'r') as log_file:
        for line in log_file:
            matches = re.findall(r'\b\d{17}\b', line)
            steam_ids.update(matches)

    # Log the collected Steam IDs
    #for steam_id in steam_ids: 

def log_get_steam_id_from_hex (hex):
    return int(hex, 16)


def execute_server_start (server):
    get_server_from_name (server).execute_server_start()

def execute_server_restart (server):
    get_server_from_name (server).execute_server_restart ()
    
def execute_server_stop (server):
    get_server_from_name (server).execute_server_stop ()

def execute_server_kill (server):
    get_server_from_name (server).execute_server_kill ()


def send_server_info ():
    global server_info, web_server_port, web_server_address
    
    # Check web server status, return if offline, not found or not used.
    if not check_web_server():
        write_to_log_error ("Failed to ping web server", method="send_server_info()")
        return
    
    server_info_dicts = [
        {
            "server_name": info.name,
            "current_game": info.current_game,
            "current_gamemode": info.current_gamemode,
            "current_checkpoint": info.current_checkpoint,
            "last_completed_objective": info.last_completed_objective,
            "previous_game": info.previous_game,
            "joined_users": list(info.joined_users),
            "disconnected_users": list(info.disconnected_users),
            "current_users": info.current_users,
            "gamemode_changes": info.gamemode_changes,
            "total_user_joins": info.total_user_joins,
            "total_user_disconnects": info.total_user_disconnects,
            "server_restarts": info.server_restarts,
            "player_deaths": info.player_deaths,
            "game_attempts": info.game_attempts,
            "server_status": info.server_status
        }
        for info in server_info
    ]
    config = read_global_config()
    json_data = json.dumps (server_info_dicts)
    url = (f"http://{web_server_address}:{web_server_port}/update_server_info")
    try:
        json_string = {"server_info": json_data}
        response = requests.post(url, json=json_string)
    except requests.exceptions.ConnectionError as e:
        if check_web_server():
            write_to_log_error (f"Unknown Web Server Error. {e}", method="send_server_info()")
            return
        else:
            write_to_log_error (f"Web server either crashed or lost connection. Attempting to reconnect.", method="send_server_info()")
            return
    except requests.exceptions.Timeout as e:
        if check_web_server():
            write_to_log_error (f"Unknown Web Server Error. {e}", method="send_server_info()")
            return
        else:
            write_to_log_error (f"Web server either crashed or lost connection. Attempting to reconnect.", method="send_server_info()")
            return

def send_new_reports (reports):
    global server_info, web_server_port, web_server_address
    
    # Check web server status, return if offline, not found or not used.
    if not check_web_server():
        write_to_log_error ("Failed to ping web server", method="send_new_reports()")
        return
    
    report_dict = [
        {
            'target': report.target,
            'target_id': report.target_id,
            'source': report.source,
            'source_id': report.source_id,
            'date': report.date,
            'reason': report.reason,
            'text': report.text,
            'hash': report.hash
        }
        for report in reports
    ]
   
    json_data = json.dumps (report_dict)
    url = (f"http://{web_server_address}:{web_server_port}/receive_new_reports")
    try:
        response = requests.post(url, json=json_data)
    except requests.exceptions.ConnectionError as e:
        if check_web_server():
            write_to_log_error (f"Unknown Web Server Error. {e}", method="send_new_reports()")
            return
        else:
            write_to_log_error (f"Web server either crashed or lost connection. Attempting to reconnect.", method="send_new_reports()")
            return
    except requests.exceptions.Timeout as e:
        if check_web_server():
            write_to_log_error (f"Unknown Web Server Error. {e}", method="send_new_reports()")
            return
        else:
            write_to_log_error (f"Web server either crashed or lost connection. Attempting to reconnect.", method="send_new_reports()")
            return

def begin_server (name):
    global server_info, servers, data_dir
    server_info_instance = ServerInfo (name)
    server_config_path = os.path.join (data_dir, f"Server_{name}", "config.ini")
    server_instance = Server(name, server_config_path, server_info_instance)
    servers.append (server_instance)
    server_info.append (server_info_instance)
    server_instance.init_server()

def create_server (formdata):
    global server_info, servers, data_dir

    server_name = formdata.get ('server_name')
    server_name_dir = os.path.join (data_dir, f"Server_{server_name}")

    try:
        os.makedirs (server_name_dir)
    except Exception as e:
        return e

    config_dir = os.path.join (data_dir, f"Server_{server_name}", "config.ini")

    generate_config (config_dir)

    try:
        config = configparser.ConfigParser()
        config.read (config_dir)

        for key, value in formdata.items():
            if config.has_option ('General', key):
                config.set ('General', key, str(value))

        with open(config_dir, 'w') as configfile:
            config.write(configfile)
    except Exception as e:
        return e

    server_info_instance = ServerInfo (server_name)
    server_instance = Server(server_name, config_dir, server_info_instance)
    servers.append (server_instance)
    server_info.append (server_info_instance)
    server_instance.create_server(formdata.get('shared_install_dir'))

    config = read_config (config_dir)
    saved_path = config['General']['saved_path_dont_touch']

    if formdata.get ('shared_install_dir'):
        server_config = os.path.join (saved_path, "Config", f"{server_name}.ini")
    else:
        server_config = os.path.join (saved_path, "Config", "ServerConfig.ini")

    try:
        config = configparser.ConfigParser()
        config.read (server_config)

        for key, value in formdata.items():
            if config.has_option ('/Game/SCPPandemic/Blueprints/GI_PandemicGameInstance.GI_PandemicGameInstance_C', key):
                config.set ('/Game/SCPPandemic/Blueprints/GI_PandemicGameInstance.GI_PandemicGameInstance_C', key, str(value))

        gameplay_config_str = config.get ('/Game/SCPPandemic/Blueprints/GI_PandemicGameInstance.GI_PandemicGameInstance_C', 'GameplayConfig')

        gameplay_config = parse_gameplay_config (gameplay_config_str)

        for key, value in formdata.items():
            if gameplay_config.get (key, None) is not None:
                gameplay_config[key] = value

        new_gameplay_config = format_gameplay_config (gameplay_config)

        config.set ('/Game/SCPPandemic/Blueprints/GI_PandemicGameInstance.GI_PandemicGameInstance_C', 'GameplayConfig', new_gameplay_config)
        
        with open(server_config, 'w') as configfile:
            config.write(configfile)
        
    except Exception as e:
        return e
    
    register_server_created (server_name)
    time.sleep (1)
    server_instance.init_server()

    return True

def parse_gameplay_config(config_str):
    config_str = config_str.strip('()')
    config_dict = {}
    for item in config_str.split(','):
        key, value = item.split('=')
        if value.lower() == 'true':
            value = True
        elif value.lower() == 'false':
            value = False
        elif '.' in value:
            value = float(value)
        else:
            value = int(value)
        config_dict[key] = value
    return config_dict

def format_gameplay_config (config_dict):
    config_str = ','.join([f'{key}={str(value) if isinstance(value, bool) else value}' for key, value in config_dict.items()])
    return f'({config_str})'

def update_server_path_name (server):
    global server_info, servers, data_dir

    server_instance = get_server_from_name (server)
    server_instance.read_server_config()
    
    server_new_name = server_instance.name
    
    if server != server_new_name:
        server_instance.server_info.name = server_new_name
        
        old_path = os.path.join (data_dir, f"Server_{server}")
        new_path = os.path.join (data_dir, f"Server_{server_new_name}")

        try:
            os.rename (old_path, new_path)
        except Exception as e:
            write_to_log_error (e, method="update_server_path_name()")

        server_instance.update_server_path_name (server_new_name)


def add_to_global_ban_list (user_id):
    global data_dir

    banlist_dir = os.path.join (data_dir, "banlist.txt")

    with open (banlist_dir, 'a') as file:
        file.write (user_id + '\n')

    update_server_banlists ()

def update_server_banlists ():
    banlist = os.path.join (data_dir, "banlist.txt")
    for server in servers:
        server_banlist = os.path.join (server.saved_file_path, "BannedIDs.ini")
        shutil.copyfile (banlist, server_banlist)

def get_server_from_name (name):
    global servers

    for server in servers:
        if (server.name == name):
            return server
    
    return None


def check_web_server ():
    global web_server_online
    global wait_for_web_server_thread

    # Check if web server is online, and re-ping it
    if web_server_online:
        if ping_web_server():
            return True
        else:
            start_wait_for_web_server_thread()
            return False
    # If web server is not currently online, double check if we need to restart a waiting thread
    else:
        start_wait_for_web_server_thread()
    
def ping_web_server ():
    global web_server_address, web_server_port
    
    config = read_global_config()

    if not config['WebServer']['web_server_enabled']:
        return False

    try:
        response = requests.get(f"http://{web_server_address}:{web_server_port}")
        if response.status_code // 100 == 2:
            return True
        else:
            write_to_log_error (f"Server returned an error: {response.status_code}", method="ping_web_server()")
            return False
    except requests.ConnectionError:
        return False
    except requests.RequestException:
        return False

def wait_for_web_server ():
    global web_server_online
    global wait_for_web_server_thread
    
    while not web_server_online:
        if ping_web_server():
            web_server_online = True
            wait_for_web_server_thread = None
        time.sleep (5)

def start_wait_for_web_server_thread():
    global wait_for_web_server_thread
    if wait_for_web_server_thread == None:
        wait_for_web_server_thread = threading.Thread(target=wait_for_web_server, daemon=True).start()


def write_to_log (server, content):
    global log_dir

    logging.info (f"{server} - {content}")

    log_file = os.path.join (log_dir, "log.txt")

    current_datetime = datetime.now()
    formatted_datetime = current_datetime.strftime("%d-%m-%Y %Hh%M")
    with open(log_file, 'a') as log:
        log.write(f"\n[{formatted_datetime}] {server} - {content}")

def write_to_log_error (content, severity: LogLevel=LogLevel.WARNING, server="", method=""):
    global log_dir 

    console_error_string = ""
    if server != "":
        console_error_string += f" {server}"
    if method != "":
        console_error_string += f" {method}"
    console_error_string += f" - {content}"

    match severity:
        case LogLevel.DEBUG:
            logging.debug (content)
        case LogLevel.INFO:
            logging.info (content)
        case LogLevel.WARNING:
            logging.warning (content)
        case LogLevel.ERROR:
            logging.error (content)
        case LogLevel.CRITICAL:
            logging.critical (content)

    log_file = os.path.join (log_dir, "log.txt")

    current_datetime = datetime.now()
    formatted_datetime = current_datetime.strftime("%d-%m-%Y %Hh%M")

    error_string = f"\n[{formatted_datetime}] [{severity.name}]"
    if server != "":
        error_string += f" {server}"
    if method != "":
        error_string += f" ({method})"
    error_string += f" - {content}"
    

    with open(log_file, 'a') as log:
        log.write(error_string)

def create_log_file():
    global log_dir

    log_file = os.path.join (log_dir, "log.txt")
    current_datetime = datetime.now()
    formatted_datetime = current_datetime.strftime("%d-%m-%Y %Hh%M")
    if not os.path.exists(log_file):
        with open(log_file, 'w') as log:
            log.write(f"[Start of log file: {formatted_datetime}]\n")
    else:
        save_log_file()
        with open(log_file, 'w') as log:
            log.write(f"[Start of log file: {formatted_datetime}]\n")

def save_log_file():
    global log_dir

    current_datetime = datetime.now()
    formatted_datetime = current_datetime.strftime("%d_%m_%Y-%Hh%M")

    log_file = os.path.join (log_dir, "log.txt")
    log_save_log = os.path.join (log_dir, f"log_{formatted_datetime}.txt")

    shutil.move(log_file, log_save_log)

    with open(log_file, 'w') as log:
        log.write("")

def read_config(config_file_path):
    if not os.path.isfile (config_file_path):
        generate_config (config_file_path)

    config = configparser.ConfigParser()
    config.read(config_file_path)    
    return config

def get_all_server_paths():
    global data_dir

    configs = []

    # Find all server folders
    server_folders = [folder for folder in os.listdir(data_dir) if os.path.isdir(os.path.join(data_dir, folder)) and folder.startswith("Server_")]

    for folder in server_folders:
        config_file_path = os.path.join(data_dir, folder, 'config.ini')
        configs.append({'folder': os.path.join (data_dir, folder), 'config': config_file_path})

    return configs
    
def generate_config(config_file_path):
    newConfig = configparser.ConfigParser()
    server_dir_name = config_file_path.replace ("Server_", "").replace ("/config.ini", "")
    newConfig['General'] = {
        'server_name': server_dir_name,
        'install_dir': 'SCP Pandemic Dedicated Server',
        'shared_install_dir': 'False',
        'saved_path_dont_touch': '',
        'max_reloads': '7',
        'starting_gamemode': '',
        'restricted_gamemode': '',
        'port': '7777',
        'queryport': '27015',
        'server_args': '',
        'active_hours': ''
    }
    newConfig['MOTD'] = {
        'motd': '',
        'join_motd': '',
        'crash_motd': 'False'
    }

    with open(config_file_path, 'w') as config_file:
        newConfig.write(config_file)

@staticmethod
def get_global_config ():
    config_dir = platformdirs.user_config_dir ("Meshed Server Tool", "Skomesh")

    config_file = os.path.join (config_dir, "config.ini")

    if not os.path.exists (config_file):
        generate_global_config ()
    
    config = configparser.ConfigParser()
    config.read (config_file)

    return config

def read_global_config ():
    global web_server_address, web_server_port

    config = get_global_config ()

    web_server_address = config['WebServer']['web_server_address']
    web_server_port = config['WebServer']['web_server_port']

    return config
    
def generate_global_config ():
    global config_dir

    config_file = os.path.join (config_dir, "config.ini")

    new_config = configparser.ConfigParser()
    new_config['WebServer'] = {
        'web_server_enabled': True,
        'web_server_address': '127.0.0.1',
        'web_server_port': 5000
    }
    new_config['General'] = {
        'log_checking_interval': 4
    }
    new_config['MOTD'] = {
        'global_server_motd': ''
    }
    with open (config_file, 'w') as config:
        new_config.write (config)


def init_sockets ():
    global web_server_address, web_server_port

    server_socket = socket.socket (socket.AF_INET, socket.SOCK_STREAM)
    server_socket.bind ((web_server_address, int (web_server_port) + 1))
    server_socket.listen(2)

    threading.Thread(target=listen_for_clients, args=(server_socket,)).start()

def listen_for_clients(server_socket): 
    while True:
        client_socket, client_address = server_socket.accept()
        threading.Thread (target=handle_client, args=(client_socket, client_address)).start()
        time.sleep (3)
        
def handle_client (client_socket, client_address):
    try:
        buffer_size = 1024
        data = b""
        client_socket.settimeout (2)
        while True:
            try:
                chunk = client_socket.recv(buffer_size)
                if not chunk:
                    break
                data += chunk
            except socket.timeout:
                break
        
        request_data = json.loads (data)

        data_server = request_data['server']
        data_action = request_data['action']

        if data_action == "start":
            execute_server_start (data_server)
        elif data_action == "restart":
            execute_server_restart (data_server)
        elif data_action == "stop":
            execute_server_stop (data_server)
        elif data_action == "kill":
            execute_server_kill (data_server)
        elif data_action == "get_server_config":
            server = get_server_from_name (data_server)
            config = server.config_path
            response = json.dumps (config)
            client_socket.sendall (response.encode('utf-8'))
        elif data_action == "create":
            formdata = request_data['formdata']
            create_server(formdata)
        elif data_action == "read_report":
            report_hash = request_data['hash']
            UserReport.handle_report (report_hash)
        elif data_action == "delete_report":
            report_hash = request_data['hash']
            UserReport.delete_report (report_hash)
        elif data_action == "ban":
            user_id = request_data['user_id']
            add_to_global_ban_list (user_id)
    except Exception as e:
        write_to_log_error (f"Error handling client {client_address}: {e}, {traceback.format_exc()}", LogLevel.ERROR, method="handle_client()")
    finally:
        client_socket.close()

def main():
    global data_dir, config_dir, log_dir

    app_name = "Meshed Server Tool"
    app_author = "Skomesh"

    data_dir = platformdirs.user_data_dir (app_name, app_author, ensure_exists=True)
    config_dir = platformdirs.user_config_dir (app_name, app_author, ensure_exists=True)
    log_dir = platformdirs.user_log_dir (app_name, app_author, ensure_exists=True)

    configs = get_all_server_paths()
    
    create_log_file()

    for config in configs:
        folder_split = config['folder'].split ('_')
        name = folder_split[1]
        begin_server (name)
    
    global_config = read_global_config()
    
    UserReport.start_report_checking_thread()

    global web_server_online
    global is_using_web_server

    is_using_web_server = ast.literal_eval(global_config['WebServer']['web_server_enabled'])
    if is_using_web_server:
        if ping_web_server():
            web_server_online = True
            init_sockets()
        else:
            start_wait_for_web_server_thread()
    
    send_server_info()

    while True:
        time.sleep(3)

if __name__ == "__main__":
    main()