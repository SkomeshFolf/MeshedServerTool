"""
    # Mesh Server Tool

    Author: Skomesh

    Version 0.2.3

    Mesh Server Tool is a Python script designed to monitor and analyze log files generated by SCP 5k game servers. 
    It keeps track of user joins and leavings and automatically restart the server.

    ## Features

    - Real-time Analysis: Continuously monitors log files for changes and provides real-time analysis.
    - Player Tracking: Tracks player joins and disconnects, by their Steam IDs.
    - Game Mode Changes: Monitors changes in the game mode and logs when transitions occur.

    ## Dependencies

    - pygtail: A Python library for tailing log files.
    - psutil: A cross-platform library for retrieving information on running processes and system utilization.

    Use this command to install the dependencies:
    pip install -r requirements.txt

    ## Configuration

    Edit the configuration file (`config.ini`) to set parameters such as log file path, maximum reloads, etc.

    ## Usage

    Run the script using Python
"""

import re
import subprocess
import time
import pygtail
import threading
import os
import configparser
import psutil

server_process = None
config = None
gamemode_changes = 0
log = None
current_line = 0
joined_users = set()
disconnected_users = set()
previous_gamemode = None
total_user_joins = 0
total_user_disconnects = 0

def analyze_log():
    global server_process
    global config
    global gamemode_changes
    global log
    global current_line
    global joined_users
    global disconnected_users
    global previous_gamemode
    global total_user_joins
    global total_user_disconnects

    config = read_config()
    max_reloads = int(config['max_reloads'])
    log_file_path = config['log_file_path']
    restricted_gamemode = config['restricted_gamemode']

    

    def output_server_info():
        print(f"Connected Users: {joined_users - disconnected_users}")
        print(f"Number of Users: {len(joined_users) - len(disconnected_users)}")
        print(f"Total User Joins: {total_user_joins}")
        print(f"Total User Disconnects: {total_user_disconnects}")
        print(f"Current Gamemode: {previous_gamemode}")
        print(f"Current Line: {current_line}")
        print(f"Gamemode Changes: {gamemode_changes}")
        print(f"Server Process: {server_process}")
        print()
        print()
    
    def async_output_server_info():
        while True:
            time.sleep(3)  
            os.system('cls' if os.name == 'nt' else 'clear')  # Clear console
            output_server_info()

    threading.Thread(target=async_output_server_info, daemon=True).start()

    
    start_server()
    
    while True:
        time.sleep(3)

        try:
            log = pygtail.Pygtail(log_file_path)
        except FileNotFoundError:
            print(f"Error: Log file {log_file_path} not found.")
            return

        while True:
            try:
                with open(log_file_path, 'r') as log_file:
                    for _ in range(current_line):
                        log_file.readline()

                    for line in log_file:
                        current_line += 1

                        game_class = current_gamemode(line)
                        if game_class:
                            if game_class != previous_gamemode:
                                gamemode_changes += 1
                                previous_gamemode = game_class
                            
                            if gamemode_changes > max_reloads:
                                restart_server()
                                break
                            elif restricted_gamemode != None and previous_gamemode != restricted_gamemode:
                                restart_server()
                                break

                        player_id = player_joined(line)
                        if player_id and player_id not in joined_users:
                            total_user_joins += 1
                            print(f"Player joined. SteamID: {player_id}")
                            joined_users.add(player_id)

                        player_id = player_leave(line)
                        if player_id and player_id not in disconnected_users:
                            total_user_disconnects += 1
                            print(f"Player left. SteamID: {player_id}")
                            disconnected_users.add(player_id)
            except FileNotFoundError:
                print(f"Error: Log file {log_file_path} not found.")
                break

def restart_server():
    stop_server()
    start_server()

def stop_server():
    global server_process
    if server_process:
        try:
            parent_pid = server_process.pid
            parent = psutil.Process(parent_pid)
            children = parent.children(recursive=True)
            for child in children:
                child.terminate()
            psutil.wait_procs(children, timeout=10)
            parent.terminate()
            parent.wait(timeout=10)
        except psutil.NoSuchProcess:
            pass
        finally:
            server_process = None
    
def start_server():
    global server_process
    global config

    reset_vars()

    if not server_process:
        executable = config['server_executable']
        server_args_raw = config['server_args']
        server_args = server_args_raw.split(',')
        command = [executable] + server_args
        server_process = subprocess.Popen(command)

def reset_vars():
    global gamemode_changes
    global log
    global current_line
    global joined_users
    global disconnected_users
    global previous_gamemode
    global total_user_joins
    global total_user_disconnects

    gamemode_changes = 0
    log = None
    current_line = 0
    joined_users = set()
    disconnected_users = set()
    previous_gamemode = None
    total_user_joins = 0
    total_user_disconnects = 0

def current_gamemode(line):
    match = re.search(r'LogBlueprintUserMessages: Map vote has concluded, travelling to (.+)', line)
    return match.group(1) if match else None

def player_joined (line):
    match = re.search(r'Sending auth result to user (\d+)', line)
    if match:
        return match.group(1)
    
    return None

def player_leave (line):
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

def track_and_log_steam_ids(log_file_path):
    steam_ids = set()

    with open(log_file_path, 'r') as log_file:
        for line in log_file:
            matches = re.findall(r'\b\d{17}\b', line)
            steam_ids.update(matches)

    # Log the collected Steam IDs
    print("All Steam IDs in the log file:")
    for steam_id in steam_ids:
        print(steam_id)

def read_config():
    config = configparser.ConfigParser()

    if not os.path.exists('config.ini'):
        generate_config()

    config.read('config.ini')
    
    return config['General']

def generate_config():
    print ('generating file')
    newConfig = configparser.ConfigParser()
    newConfig['General'] = {
        'log_file_path': 'Pandemic/Saved/Logs/Pandemic.log',
        'max_reloads': '7',
        'restricted_gamemode': '',
        'server_executable': '',
        'server_args': ''
    }

    with open('config.ini', 'w') as config_file:
        newConfig.write(config_file)

def main():
    analyze_log()
    
if __name__ == "__main__":
    main()