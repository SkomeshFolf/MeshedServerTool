"""
    # Mesh Server Tool

    Author: Skomesh
    Version 1.1.0

    Mesh Server Tool is a Python script designed to monitor and analyze log files generated by SCP 5k game servers. 
    It keeps track of user joins and leavings and automatically restart the server.

    ## Features

    - Real-time Analysis: Continuously monitors log files for changes and provides real-time analysis.
    - Player Tracking: Tracks player joins and disconnects, by their Steam IDs.
    - Game Mode Changes: Monitors changes in the game mode and logs when transitions occur.

    ## Dependencies

    - pygtail: A Python library for tailing log files.
    - psutil: A cross-platform library for retrieving information on running processes and system utilization.
    - flask: Web framework for the local web server.
    - flask-bootstrap: Web framework for the local web server.
    - gunicorn: Web framework for the local web server.
    - requests: Used to web server monitoring.

    Use this command to install the dependencies:
    pip install -r requirements.txt

    ## Configuration

    General configuration:
    Launching the server management tool will create a config.ini file. 
    The following are the parameters in the newly created config.ini file:
        [General]
            - log_checking_interval:    How often should the log file have new lines be evaluated?
                                        Lower means higher frequency, but higher system usage.
        [WebServer]
            - web_server_enabled:       Can be true or false. If true, the tool assumes that the monitoring web server is active and will use it.
            - web_server_address:       Custom address of the web server. If hosted on same machine, this is usually '127.0.0.1'.
            - port:                     Port of the web server. By default is 5000.
        [MOTD]
            - global_server_motd:       An MOTD message that will be used in every server instance.

    Server configuration:
    Create a directory with name: Server_[server name].
    Launching the server manager will create a new config file for the server.
    The following are the parameters in the newly created config.ini file
        - log_file_path:        The directory of the server's log file. The log file should be Pandemic.log.
        - saved_directory_path: The directory to the "Saved" folder. From the root folder, should be in Pandemic/Saved
        - max_reloads:          How many map changes should lead to a full server restart.
        - restricted_gamemode:  Should the server be restricted to only a single gamemode? Leave blank to allow map switching.
        - server_executable:    The directory to the actual server executable. Should be PandemicServer.exe on windows, or PandemicServer on linux.
                                    The executable should be in /Pandemic/Binaries/Linux OR WindowsServer/PandemicServer.exe
        - server_args:          Any arguments to be passed to the server, such as -port and -queryport. Each argument separated by commas
                                    ex. M_WaveSurvival,-port=7777,-queryport=27015
        - monitor_only:         Should the script manage the server by starting and stopping the server, or just monitor logs?
        - active_hours:         What time should the server be active for? In format HH:MM-HH:MM (ex 08:00-18:00)
        - server_motd:          What should be the server's MOTD be. In format of array.

    ## Usage

    Run the script using Python. Run the following command in CMD or Terminal:
    python3 MeshServer.py

    ## TODO 
    - Add a web interface [80%]
    - Aggregate user reports 
        - Use hashing for reports
    - Implement a global banning system
    - Implement a logging system [X]
    - Implement server configuration from web interface
    - Implement ways to shutdown and restart servers through the web interface
    - Implement server messages to indicate restart times and other MOTD stuff
"""

import re
import subprocess
import time
import pygtail
import threading
import requests
import json
import os
import configparser
import psutil
import ast
from datetime import datetime, time, timedelta
import time
import shutil

class Server:
    def __init__(self, name, config, server_info):
        self.name = name
        self.server_info = server_info

        self.log_file_path = config['log_file_path']
        self.saved_file_path = config['saved_file_path']
        self.max_reloads = int(config['max_reloads'])
        self.server_executable = config['server_executable']
        self.restricted_gamemode = config['restricted_gamemode']
        self.server_args = config['server_args']
        self.monitor_only = ast.literal_eval(config['monitor_only'])
        if (config['active_hours'] == ''):
            self.active_hours = False
        else:
            timeSplit = config['active_hours'].split("-")
            self.active_hours = True
            self.start_time = datetime.strptime (timeSplit[0], '%H:%M').time()
            self.end_time = datetime.strptime (timeSplit[1], '%H:%M').time()
        
        self.motd = config['server_motd']

        self.server_process = None
        self.log = None
        self.config = config
        self.server_started = False

        self.current_line = 0
        self.idle_time = -1
        self.log_check_interval = read_global_config()['General']['log_checking_interval']
        self.last_crash = None

        self.lock = threading.Lock()

    def init_server (self):
        self.start_server()
        self.start_log_analysis()

    def start_log_analysis (self):
        thread = threading.Thread (target=self.analyze_log, daemon=True)
        thread.start()

    def start_server (self):
        self.server_info.server_status_change (2)
        register_server_start (self.name)
        self.reset_vars()
        self.launch_server()
        
    def launch_server (self):
        if not self.server_process:
            server_args_raw = self.server_args
            server_args = server_args_raw.split(',')
            command = [self.server_executable] + server_args
            self.server_process = subprocess.Popen(command)
            self.server_info.server_restarts = self.server_info.server_restarts + 1
            self.server_info.server_status_change (5)

    def init_motd (self):
        if (self.motd and self.motd != ''):
            path = self.saved_file_path
            global_motd = read_global_config['MOTD']['global_server_motd']
            with open(f"{path}/Messages.ini", 'w') as message_file:
                message_file.write(f"{global_motd},2,00:00:05")
                interval = 8
                for message in self.motd:
                    message_file.write (f"{message},2,00:00:{interval}")
                    interval += 3
                if self.last_crash not None:
                    message_file.write (f"The last server crashed was at: {self.last_crash}. Lets hope it doesn't crash again!, 2, 00:00:{interval}")
                    interval += 3
                if self.active_hours:
                    message_file.write (f"The server will be shutdown in one hour., 1, {self.end_time - timedelta(hours=1)}:00")
                    message_file.write (f"The server will be shutdown in 30 minutes., 1, {self.end_time - timedelta(minutes=30)}:00")
                    message_file.write (f"The server will be shutdown after this game ends., 1, {self.end_time}:00")

    
    def wake_server (self):
        self.server_info.server_status_change (1)
        register_server_wake (self.name)
        self.start_server()
        self.start_log_analysis()

    def active_server (self):
        if self.server_info.server_status != 5:
            self.server_info.server_status_change (5)
            register_server_active (self.name)

    def stop_server(self):
        self.server_info.server_status_change (0)
        register_server_stop (self.name)
        if self.server_process:
            self.kill_server()
                
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
            except psutil.NoSuchProcess:
                pass
            finally:
                self.server_process = None

    def suspend_server(self):
        self.stop_server()
        self.server_info.server_status_change (-1)
        self.reset_vars()
        register_server_suspend (self.name)

        while True:
            current_time = datetime.now().time()
            if self.start_time <= current_time <= self.end_time:
                break

            time.sleep (60)
        
        self.wake_server()

    def restart_server(self, reason):
        self.server_info.server_status_change (3)
        register_server_restart (self.name, reason)
        if not reason:
            self.init_motd()

        self.stop_server()
        self.start_server()
        
    def idle_server (self):
        self.idle_time = time.time()
        self.server_info.server_status_change (4)
        register_server_idle (self.name)

    def server_crashed (self, monitor_only):
        print ("Server crashed")
        self.last_crash = datetime.now().time()
        self.init_motd ()
        self.server_info.server_status_change (-2)
        if not monitor_only:
            self.restart_server ("Server crash")

    def reset_vars(self):
        self.log = None
        self.server_info.previous_gamemode = None
        self.server_info.joined_users = set()
        self.server_info.disconnected_users = set()
        self.server_info.current_users = set()
        self.server_info.total_user_joins = 0
        self.server_info.total_user_disconnects = 0
        self.server_info.gamemode_changes = 0
        self.current_line = 0
        self.last_crash = None
   
    def is_active_hours (self):
        current_time = datetime.now().time()
        if self.start_time <= current_time <= self.end_time:
            return True
        else:
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
            while server_logging:
                time.sleep(3)

                # Setup the log file for reading
                try:
                    self.log = pygtail.Pygtail(self.log_file_path)
                except FileNotFoundError:
                    print(f"Error: Log file {self.log_file_path} not found.")
                    return
                
                # Keep executing while the server is active.
                # Server will be labeled inactive if the server restarts, suspends or crashes.
                server_active = True
                while server_active:

                    # Check if the server has crashed
                    if self.server_process.poll() != None:
                        self.server_crashed(self.monitor_only)
                        server_active = False
                        break

                    # Has the server been idle for 5 minutes? Should the server restart anyways?
                    if self.idle_time > 0:
                        if self.gamemode_changes > 1:
                            if time.time() > self.idle_time + 300:
                                self.restart_server("Server idle")
                        
                    # Go through each new line 
                    with open(self.log_file_path, 'r') as log_file:
                        for _ in range(self.current_line):
                            log_file.readline()

                        for line in log_file:
                            self.current_line += 1

                            game_class = log_is_new_gamemode(line)
                            if game_class:
                                # Check if new gamemode is an idle state
                                if game_class == "Entry":
                                    self.idle_server()
                                else:
                                    self.server_info.gamemode_change (game_class)

                                # Suspend the server if beyond active hours
                                if self.active_hours:
                                    if not self.is_active_hours ():
                                        self.suspend_server()
                                        server_active = False
                                        server_logging = False
                                        break

                                # Check if server should restart for either max gamemode changes or loading the wrong gamemode.
                                if not self.monitor_only:
                                    if self.server_info.gamemode_changes > self.max_reloads:
                                        self.restart_server(f"Server reloaded {self.server_info.gamemode_changes} times")
                                        server_active = False
                                        break
                                    elif self.restricted_gamemode != '' and self.server_info.previous_gamemode != self.restricted_gamemode:
                                        self.restart_server(f"Server loaded a gamemode that is not {self.restricted_gamemode}")
                                        server_active = False
                                        break

                                # Declare the server is active
                                self.active_server ()

                            # Is the latest log a player joining? Log it in the server info.
                            player_id = log_is_player_joined(line)
                            if player_id:
                                self.server_info.player_join(player_id)

                            # Is the latest log a player leaving? Log it in the server info.
                            player_id = log_is_player_leave(line)
                            if player_id:
                                self.server_info.player_leave(player_id)

                            # Is the latest log the server entering an idle state? Enter idle state.
                            server_idle = log_is_entering_idle (line)
                            if server_idle:
                                self.idle_server()

                            # Is this the first time the server has started? Init the server.
                            if not self.server_started:
                                start_mode = log_is_starting_gamemode (line)
                                if start_mode:
                                    self.server_info.gamemode_change (start_mode)
                                    register_server_gamemode (self.name, start_mode)
                                    self.server_started = True

                    # TODO: Handle reports
                    send_server_info ()
                    time.sleep(self.log_check_interval)

class ServerInfo:
    def __init__ (self, name):
        self.server_name = name
        self.previous_gamemode = None
        self.joined_users = set()
        self.disconnected_users = set()
        self.current_users = set()
        self.gamemode_changes = 0
        self.total_user_joins = 0
        self.total_user_disconnects = 0
        self.server_restarts = 0
        self.server_status = 'Offline'
    
    def player_join (self, player):
        self.total_user_joins += 1
        self.joined_users.add(player)
        self.current_users.add(player)
        register_player_join (self.server_name, player)
    
    def player_leave (self, player):
        self.total_user_disconnects += 1
        self.disconnected_users.add(player)
        self.current_users.discard(player)
        register_player_leave (self.server_name, player)

    def gamemode_change (self, gamemode):
        if gamemode != self.previous_gamemode:
            self.gamemode_changes += 1
            self.previous_gamemode = gamemode
    
    def reset_variables (self):
        self.previous_gamemode = None
        self.joined_users = set()
        self.disconnected_users = set()
        self.current_users = set()
        self.gamemode_changes = 0
        self.total_user_joins = 0
        self.total_user_disconnects = 0
    
    def server_status_change (self, new_status):
        status_dict = {
            -3: 'Offline',
            -2: 'Crashed',
            -1: 'Suspended',
            0: 'Stopping',
            1: 'Waking',
            2: 'Starting',
            3: 'Restarting',
            4: 'Idle',
            5: 'Active'
        }
        self.server_status = status_dict.get (new_status, 'Offline')
    
    def __repr__(self):
        return f"ServerInfo(server_name={self.server_name}, " \
               f"previous_gamemode={self.previous_gamemode}, " \
               f"joined_users={self.joined_users}, " \
               f"disconnected_users={self.disconnected_users}, " \
               f"current_users={self.current_users}, " \
               f"gamemode_changes={self.gamemode_changes}, " \
               f"total_user_joins={self.total_user_joins}, " \
               f"total_user_disconnects={self.total_user_disconnects}, " \
               f"server_restarts={self.server_restarts}, " \
               f"server_status={self.server_status})"
    
class UserReport:
    def __init__ (self, server, target, target_id, source, source_id, date, reason, text):
        self.server = server
        self.target = target
        self.target_id = target_id
        self.source = source
        self.source_id = source_id
        self.date = date
        self.reason = reason
        self.text = text


servers = []
server_info = []
main_log_file = None
is_using_web_server = False
web_server_online = False
wait_for_web_server_thread = None

def register_player_join (server, player):
    print (f"Player {player} has joined {server}")
    write_to_log (server, f"Player {player} connected.")
    send_server_info ()

def register_player_leave (server, player):
    print (f"Player {player} has left {server}")
    write_to_log (server, f"Player {player} has disconnected.")
    send_server_info ()

def register_server_restart (server, reason):
    print (f"Server {server} has restarted for: {reason}.")
    write_to_log (server, f"Server restarted for: {reason}.")
    send_server_info ()

def register_server_gamemode (server, gamemode):
    print (f"Server {server} has changed gamemode to {gamemode}")
    write_to_log (server, f"Gamemode changed to {gamemode}.")
    send_server_info ()

def register_server_start (server):
    print (f"Server {server} has started")
    write_to_log (server, f"Server started.")
    send_server_info ()

def register_server_active (server):
    print (f"Server {server} is now active")
    write_to_log (server, f"Server active.")
    send_server_info ()

def register_server_stop (server):
    print (f"Server {server} has stopped")
    write_to_log (server, f"Server stopped.")
    send_server_info ()

def register_server_suspend (server):
    print (f"Server {server} has suspended")
    write_to_log (server, "Server suspended.")
    send_server_info ()

def register_server_wake (server):
    print (f"Server {server} woke from suspending")
    write_to_log (server, "Server waking from suspension.")
    send_server_info ()

def register_server_idle (server):
    print (f"Server {server} is now idle.")
    write_to_log (server, "Server is now idle.")
    send_server_info ()

def register_web_server_error (e):
    print (f"Web Server Error: {e}")
    write_to_log ("Web Server", f"Web Server Error: {e}")

def send_server_info ():
    global server_info
    global web_server_address
    
    # Check web server status, return if offline, not found or not used.
    if not check_web_server():
        return
    
    server_info_dicts = [
    {
        "server_name": info.server_name,
        "previous_gamemode": info.previous_gamemode,
        "joined_users": list(info.joined_users),
        "disconnected_users": list(info.disconnected_users),
        "current_users": list(info.current_users),
        "gamemode_changes": info.gamemode_changes,
        "total_user_joins": info.total_user_joins,
        "total_user_disconnects": info.total_user_disconnects,
        "server_restarts": info.server_restarts,
        "server_status": info.server_status
    }
    for info in server_info
    ]
    config = read_global_config()
    json_data = json.dumps (server_info_dicts)
    url = (f"http://{config['WebServer']['web_server_address']}:{config['WebServer']['web_server_port']}/update_server_info")
    try:
        response = requests.post(url, json={"server_info": json_data})
    except requests.exceptions.ConnectionError as e:
        if check_web_server():
            register_web_server_error (f"Unknown Web Server Error. {e}")
            return
        else:
            register_web_server_error (f"Web server either crashed or lost connection. Attempting to reconnect.")
            return
    except requests.exceptions.Timeout as e:
        if check_web_server():
            register_web_server_error (f"Unknown Web Server Error. {e}")
            return
        else:
            register_web_server_error (f"Web server either crashed or lost connection. Attempting to reconnect.")
            return


def output_server_info():
    global servers

    for server in servers:
        server_info = server.server_info
        print(f"{server.name}")
        print(f"\tConnected Users: {server_info.current_users}")
        print(f"\tNumber of Users: {len(server_info.joined_users) - len(server_info.disconnected_users)}")
        print(f"\tTotal User Joins: {server_info.total_user_joins}")
        print(f"\tTotal User Disconnects: {server_info.total_user_disconnects}")
        print(f"\tCurrent Gamemode: {server_info.previous_gamemode}")
        print(f"\tGamemode Changes: {server_info.gamemode_changes}")
        print(f"\tServer Restarts: {server_info.server_restarts}")
        print()

def begin_server (config, name):
    global server_info
    server_instance_info = ServerInfo (name)
    server_instance = Server(name, config, server_instance_info)
    servers.append (server_instance)
    server_info.append (server_instance_info)
    server_instance.init_server()

def log_is_new_gamemode(line):
    match = re.search(r'Map vote has concluded, travelling to (.+)', line)
    if match: 
        return match.group(1) if match else None

def log_is_starting_gamemode (line):
    match = re.search (r'LogLoad: LoadMap: /Game/SCPPandemic/Maps/([^/]+)/', line)
    if match:
        return match.group(1) if match else None

def log_is_entering_idle (line):
    match = re.search (r'Entering Standby, going to standby map M_ServerDefault.', line)
    return bool (match)

def log_is_player_joined (line):
    match = re.search(r'Sending auth result to user (\d+)', line)
    if match:
        return match.group(1)
    
    return None

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
    print("All Steam IDs in the log file:")
    for steam_id in steam_ids:
        print(steam_id)

def check_reports (saved_file_path, server):
    reports = []

    try:
        # Iterate over all files in the directory
        for filename in os.listdir(f"{saved_file_path}/Reports"):
            file_path = os.path.join(f"{saved_file_path}/Reports", filename)

            # Check if the path is a file
            if os.path.isfile(file_path):
                print(f"Reading and parsing contents of file: {filename}")

                # Read and parse the contents of the file
                with open(file_path, 'r') as file:
                    file_contents = file.read()
                    report = parse_report(file_contents, server)
                    if not check_report_handled(report):
                        reports.append(report)

    except Exception as e:
        print(f"An error occurred: {e}")

    return reports

def check_web_server ():
    global is_using_web_server
    global web_server_online
    global wait_for_web_server_thread

    # Is web server used
    if is_using_web_server:    
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
            return False
    else:
        return False
    
def ping_web_server ():
    config = read_global_config()

    if not config['WebServer']['web_server_enabled']:
        return False

    try:
        response = requests.get(f"http://{config['WebServer']['web_server_address']}:{config['WebServer']['port']}")
        if response.status_code // 100 == 2:
            return True
        else:
            print(f"Server returned an error: {response.status_code}")
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
        time.sleep (30)

def start_wait_for_web_server_thread():
    global wait_for_web_server_thread
    if wait_for_web_server_thread == None:
        wait_for_web_server_thread = threading.Thread(target=wait_for_web_server, daemon=True).start()

def parse_report(file_contents, server):
    # Split the lines of the file
    lines = file_contents.split('\n')

    # Parse individual fields based on line number
    target_id, target = lines[0].split(',')
    source_id, source = lines[1].split(',')
    date_str = lines[2].strip()
    date = datetime.strptime(date_str, '%Y.%m.%d-%H.%M.%S')
    reason = lines[4].strip()
    text = lines[5].strip()

    return UserReport(server, target, target_id, source, source_id, date, reason, text)

#def check_report_handled (report):

def write_to_log (server, content):
    current_datetime = datetime.now()
    formatted_datetime = current_datetime.strftime("%d-%m-%Y %H:%M:%S")
    with open("log.txt", 'a') as log_file:
        log_file.write(f"\n[{formatted_datetime}] {server} - {content}")

def create_log_file():
    current_datetime = datetime.now()
    formatted_datetime = current_datetime.strftime("%d-%m-%Y %H_%M_%S")
    if not os.path.exists("log.txt"):
        with open("log.txt", 'w') as log_file:
            log_file.write(f"[Start of log file: {formatted_datetime}]\n")
    else:
        save_log_file()
        with open("log.txt", 'w') as log_file:
            log_file.write(f"[Start of log file: {formatted_datetime}]\n")

def save_log_file():
    if not os.path.exists("Logs"):
        os.makedirs("Logs")

    current_datetime = datetime.now()
    formatted_datetime = current_datetime.strftime("%d_%m_%Y-%H.%M.%S")

    shutil.move("log.txt", f"Logs/log_{formatted_datetime}.txt")

    with open("log.txt", 'w') as log_file:
        log_file.write("")    

def read_config(config_file_path):
    config = configparser.ConfigParser()
    config.read(config_file_path)    
    return config['General']

def get_server_configs():
    configs = []
    # Find all server folders
    server_folders = [folder for folder in os.listdir() if os.path.isdir(folder) and folder.startswith("Server_")]

    for folder in server_folders:
        config_file_path = os.path.join(folder, 'config.ini')
        if os.path.exists(config_file_path):
            config = read_config(config_file_path)
            configs.append({'folder': folder, 'config': config})
        else:
            generate_config (config_file_path)
            config = read_config(config_file_path)
            configs.append({'folder': folder, 'config': config})

    return configs

def generate_config(config_file_path):
    newConfig = configparser.ConfigParser()
    newConfig['General'] = {
        'log_file_path': '../Pandemic/Saved/Logs/Pandemic.log',
        'saved_file_path': '../Pandemic/Saved',
        'max_reloads': '7',
        'restricted_gamemode': '',
        'server_executable': '',
        'server_args': '',
        'monitor_only': False,
        'active_hours': '',
        'server_motd': ''
    }

    with open(config_file_path, 'w') as config_file:
        newConfig.write(config_file)
        
def read_global_config ():
    if not os.path.exists ("config.ini"):
        generate_global_config ()
    
    config = configparser.ConfigParser()
    config.read ("config.ini")
    return config
    
def generate_global_config ():
    new_config = configparser.ConfigParser()
    new_config['WebServer'] = {
        'web_server_enabled': True,
        'web_server_address': '127.0.0.1',
        'web_server_port': 5000
    }
    new_config['General'] = {
        'log_checking_interval': 7
    }
    new_config['MOTD'] = {
        'global_server_motd': ''
    }
    with open ("config.ini", 'w') as config_file:
        new_config.write (config_file)

def async_output_server_info():
    while True:
        time.sleep(10)  
        #os.system('cls' if os.name == 'nt' else 'clear')  # Clear console
        output_server_info()
    
def main():
    configs = get_server_configs()
    
    create_log_file()

    for config in configs:
        name = config['folder']
        config = config['config']
        begin_server (config, name)

    #threading.Thread(target=async_output_server_info, daemon=True).start()
    
    global_config = read_global_config()
    
    global web_server_online
    global is_using_web_server

    is_using_web_server = ast.literal_eval(global_config['WebServer']['web_server_enabled'])
    if is_using_web_server:
        if ping_web_server():
            web_server_online = True
        else:
            start_wait_for_web_server_thread()

    while True:
        time.sleep(3)

if __name__ == "__main__":
    main()