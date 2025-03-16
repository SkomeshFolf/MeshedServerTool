import logging
from datetime import datetime
import os
from enum import Enum
import shutil
import platformdirs

class LogLevel (Enum):
    DEBUG = 10
    INFO = 20
    WARNING = 30
    ERROR = 40
    CRITICAL = 50

def register_player_join (server, player, player_name):
    write_to_log (server, f"Player {player_name} [{player}] connected.")

def register_player_leave (server, player, player_name):
    write_to_log (server, f"Player {player_name} [{player}] has disconnected.")

def register_server_restart (server, reason):
    write_to_log (server, f"Server restarted for: {reason}.")
    
def register_server_start (server):
    write_to_log (server, f"Server started.")

def register_server_active (server):
    write_to_log (server, f"Server active.")

def register_server_stop (server):
    write_to_log (server, f"Server stopped.")

def register_server_offline (server):
    write_to_log (server, f"Server offline.")

def register_server_suspend (server):
    write_to_log (server, "Server suspended.")

def register_server_wake (server):
    write_to_log (server, "Server waking from suspension.")

def register_server_idle (server):
    write_to_log (server, "Server is now idle.")

def register_server_creating (server):
    write_to_log (server, "Server is being created for the first time.")

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

def write_to_log (server, content):
    log_dir = get_log_dir()

    logging.info (f"{server} - {content}")

    log_file = os.path.join (log_dir, "log.txt")

    current_datetime = datetime.now()
    formatted_datetime = current_datetime.strftime("%d-%m-%Y %Hh%M")
    with open(log_file, 'a') as log:
        log.write(f"\n[{formatted_datetime}] {server} - {content}")

def write_to_log_error (content, severity_int: LogLevel=30, server="", method=""):
    log_dir = get_log_dir()

    severity = LogLevel (severity_int)

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

    error_string = f"\n[{formatted_datetime}] [{severity.name}] {server} - {content}. ({method})"
    console_error_string = f"[{severity.name}] {server} - {content} ({method})"
    
    print (console_error_string)
    with open(log_file, 'a') as log:
        log.write(error_string)

def create_log_file():
    log_dir = get_log_dir ()
    log_file = os.path.join (log_dir, "log.txt")
    current_datetime = datetime.now()
    formatted_datetime = current_datetime.strftime("%d-%m-%Y %Hh%M")

    if os.path.exists (log_file):
        save_log_file()
    
    with open(log_file, 'w') as log:
        log.write(f"[Start of log file: {formatted_datetime}]\n")

def save_log_file():
    log_dir = get_log_dir()
    current_datetime = datetime.now()
    formatted_datetime = current_datetime.strftime("%d_%m_%Y-%Hh%M")

    log_file = os.path.join (log_dir, "log.txt")
    log_save_log = os.path.join (log_dir, f"log_{formatted_datetime}.txt")

    shutil.move(log_file, log_save_log)

    with open(log_file, 'w') as log:
        log.write("")

def get_log_dir(): 
    app_name = "Meshed Server Tool"
    app_author = "Skomesh"

    log_dir = platformdirs.user_log_dir (app_name, app_author, ensure_exists=True)

    return log_dir