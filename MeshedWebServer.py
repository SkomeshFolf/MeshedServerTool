from flask import Flask, request, Response, render_template, jsonify, g, redirect, url_for
from flask_login import LoginManager, UserMixin, login_user, login_required, logout_user, current_user
import json
import os
import configparser
import threading
import secrets
import math
import time
import platformdirs
import logging
import waitress
import re
import subprocess
import time
import pygtail
import threading
import json
import os
import platform
import configparser
import psutil
import ast
from datetime import datetime, time, timedelta
import time
import shutil
import platformdirs
import MeshedLogging
import MeshedRegex
import MeshedReports

#region Secret Key Creation
app_name = "Meshed Server Tool"
app_author = "Skomesh"

data_dir = platformdirs.user_data_dir (app_name, app_author, ensure_exists=True)
secret_key_file = os.path.join (data_dir, 'secret_key')

if not os.path.exists (secret_key_file):
    with open (secret_key_file, 'w') as file:
        file.write (secrets.token_hex (32))

with open (secret_key_file, 'r') as file:
    secret_key = file.read().strip()
#endregion

app = Flask(__name__)

#region Flask globals definition
app.config['servers'] = {}
app.config['reports'] = []
app.config['reports_per_user'] = {}
app.config['lock'] = threading.Lock ()
app.secret_key = secret_key
#endregion

#region Flask user setup
login_manager = LoginManager ()
login_manager.init_app (app)
login_manager.login_view = 'web_server_login'

def load_users ():
    user_file = os.path.join (data_dir, 'users.json')
    if os.path.exists (user_file):
        with open (user_file, 'r') as file:
            return json.load (file)
    else:
        return {}

users = load_users()

class User(UserMixin):
    def __init__ (self, username):
        self.id = username

@login_manager.user_loader
def load_user (user_id):
    global users
    if user_id in users:
        return User (user_id)
    return None
#endregion

#region Exceptions definitions
class OSErrorDetectionError (Exception):
    def __init__ (self, message="Either unable to detect the current OS or current OS is not supported."):
        self.message = message
        super().__init__(self.message)
#endregion

#region Globals definition
servers = []
server_info = []
report_thread = None
#endregion

#region Streams

@app.route('/stream-server-info')
@login_required
def stream_server_info():
    def generate():
        with app.app_context():
            while True:
                set_servers()
                server_info = get_servers()
                server_info_dicts = {
                    server_name: {
                        'server_name': server.name,
                        'current_users': server.current_users,
                        'server_status': server.server_status,
                        'gamemode_changes': server.gamemode_changes,
                        'server_restarts' : server.server_restarts,
                        'current_game': server.current_game,
                        'current_gamemode': server.current_gamemode,
                        'previous_game': server.previous_game,
                        'current_checkpoint': server.current_checkpoint,
                        'last_completed_objective': server.last_completed_objective,
                        'player_deaths': server.player_deaths,
                        'game_attempts': server.game_attempts,
                        'user_reports': get_reports_per_user()
                    }
                    for server_name, server in server_info.items()
                }
                yield f"data: {json.dumps(server_info_dicts)}\n\n"

                time.sleep(1)

    return Response(generate(), mimetype='text/event-stream')

@app.route('/stream-server-info-encoded')
@login_required
def stream_server_info_encoded():
    def generate():
        with app.app_context():
            while True:
                server_info = get_encoded_servers()
                yield f"data: {json.dumps(server_info)}\n\n"

                time.sleep(1)

    return Response(generate(), mimetype='text/event-stream')

@app.route('/stream-all-server-logs')
@login_required
def stream_all_server_logs():
    def generate():
        with app.app_context():
            while True:
                logs = get_logs()
                yield f"data: {json.dumps(logs)}\n\n"

                time.sleep (2)
    return Response(generate(), mimetype='text/event-stream')

@app.route('/server/<server_name>/stream-server-logs')
@login_required
def stream_server_logs(server_name):
    def generate():
        with app.app_context():
            while True:
                logs = get_logs(server=server_name)
                yield f"data: {json.dumps(logs)}\n\n"

                time.sleep (2)
    return Response(generate(), mimetype='text/event-stream')

@app.route ('/stream-new-reports-quantity')
@login_required
def stream_new_reports_quantity ():
    def generate():
        with app.app_context():
            while True:
                lock = get_lock()
                with lock:
                    yield f"data: {json.dumps(len (app.config['reports']))}\n\n"

                time.sleep (5)
    return Response (generate(), mimetype='text/event-stream')

@app.route ('/stream-new-reports')
@login_required
def stream_new_reports ():
    def generate():
        with app.app_context():
            while True:
                lock = get_lock()
                with lock:
                    yield f"data: {json.dumps(app.config['reports'])}\n\n"

                time.sleep (7)
    return Response (generate(), mimetype='text/event-stream')

#endregion

#region Routes: pages
@app.route('/')
@login_required
def web_server_home ():
    return render_template('index.html', servers=get_encoded_servers(), logs=get_logs())

@app.route('/login', methods=['POST', 'GET'])
def web_server_login ():
    global users
    if request.method == 'POST':
        username = request.form['username']
        password = request.form['password']
        if username in users and users[username]['password'] == password:
            user = User (username)
            login_user (user)
            return redirect (url_for ('web_server_home'))
        return render_template ('login.html'), 401
    if users:
        return render_template ('login.html')
    else:
        return redirect (url_for ('web_server_create_user'))

@app.route ('/create-user', methods=['POST', 'GET'])
def web_server_create_user ():
    global users
    user_file = os.path.join (data_dir, "users.json")
    if users:
        if current_user.is_authenticated:
            if request.method == 'POST':
                username = request.form['username']
                password = request.form['password']
                users[username] = {
                    'password': password
                }
                user_json = json.dumps (users)
                with open (user_file, 'w') as file:
                    file.write (user_json)

                return redirect (url_for ('web_server_home'))
            return render_template ('create_user.html')
        else:
            return redirect (url_for ('web_server_home'))
    else:
        if request.method == 'POST':
            username = request.form['username']
            password = request.form['password']
            users[username] = {
                'password': password
            }
            user_json = json.dumps (users)
            with open (user_file, 'w') as file:
                file.write (user_json)
                
            users = load_users()
            return redirect (url_for ('web_server_login'))
        return render_template ('create_user.html')

@app.route('/logout')
@login_required
def web_server_logout():
    logout_user()
    return redirect (url_for ('web_server_login'))

@app.route('/reports')
@login_required
def web_server_reports ():
    return render_template('user_reports.html', reports=app.config['reports'])

@app.route('/server/<server_name>')
@login_required
def web_server_server_page(server_name):
    set_servers()
    server_info = get_servers()

    # Find the server with the matching name in the list
    matching_servers = [server for server in server_info.values() if server.server_name == server_name]

    if matching_servers:
        # Use the first matching server (assuming server names are unique)
        return render_template('server.html', server=matching_servers[0])
    else:
        return render_template ('404_server.html')

@app.route ('/create-server')
@login_required
def web_server_create_server_page ():
    return render_template ('create_server.html')

@app.route ('/steamcmd-guide')
@login_required
def steamcmd_guide ():
    return render_template ('steamcmd_guide.html')

@app.route ('/logs/<page>')
@login_required
def web_server_logs_page (page=1):
    return render_template ('logs.html', page=page)

@app.route ('/logs/page-size', methods=['GET'])
@login_required
def get_log_pages ():
    page_size = int (request.args.get('page_size'))
    return jsonify (read_log_pages (page_size)), 200

@app.route ('/logs', methods=['GET'])
@login_required
def get_page_logs ():
    page = int (request.args.get ('page'))
    page_size = int(request.args.get ('page_size'))
    return jsonify (get_logs (line_count=page_size, start_range=(page - 1) * page_size)), 200

@app.route ('/control-server', methods=['PUT'])
@login_required
def control_server ():
    action = request.form.get("action")
    server = request.form.get("server")

    if action == "start":
        server_command_execute_server_start (server)
    elif action == "restart": 
        server_command_execute_server_restart (server)
    elif action == "stop":
        server_command_execute_server_stop (server)
    elif action == "kill":
        server_command_execute_server_kill (server)

    return jsonify ({"status": "success"}), 200

#endregion

#region Routes: Server settings
@app.route('/server/<server_name>/management-settings', methods=['GET'])
@login_required
def request_management_settings(server_name):
    return jsonify(get_management_settings(server_name))

@app.route('/server/<server_name>/players-settings', methods=['GET'])
@login_required
def request_players_settings(server_name):
    return jsonify(get_players_settings(server_name))

@app.route('/server/<server_name>/server-settings', methods=['GET'])
@login_required
def request_server_settings(server_name):
    return jsonify(get_server_settings(server_name))

@app.route('/server/<server_name>/gameplay-settings', methods=['GET'])
@login_required
def request_gameplay_settings(server_name):
    return jsonify(get_gameplay_settings(server_name))
    
@app.route('/server/<server_name>/management-settings', methods=['PUT'])
@login_required
def submit_management_settings (server_name):
    try:
        result = apply_management_settings (server_name, request.get_json())
        return result
    except Exception as e:
        return jsonify({"status": "error", "message": str(e)}), 500

@app.route('/server/<server_name>/players-settings', methods=['PUT'])
@login_required
def submit_players_settings (server_name):
    try:
        result = apply_players_settings (server_name, request.get_json())
        return result
    except Exception as e:
        return jsonify({"status": "error", "message": str(e)}), 500

@app.route('/server/<server_name>/server-settings', methods=['PUT'])
@login_required
def submit_server_settings (server_name):
    try:
        result = apply_server_settings (server_name, request.get_json())
        return result
    except Exception as e:
        return jsonify({"status": "error", "message": str(e)}), 500

@app.route('/server/<server_name>/gameplay-settings', methods=['PUT'])
@login_required
def submit_gameplay_settings (server_name):
    try:
        result = apply_gameplay_settings (server_name, request.get_json())
        return result
    except Exception as e:
        return jsonify({"status": "error", "message": str(e)}), 500

#endregion

#region Routes: Servers and reports

@app.route ('/servers', methods=['POST'])
@login_required
def submit_new_server():
    settings = request.get_json()

    create_server (settings)

    return jsonify ({"status": "success"}), 200
    
@app.route ('/reports', methods=['POST'])
@login_required
def reports_ban_user():
    data = request.get_data (as_text=True)

    add_user_to_global_ban_list (data)

    return jsonify ({"status": "success"}), 200

@app.route ('/reports', methods=['DELETE'])
@login_required
def reports_delete_report ():
    data = request.get_data (as_text=True)

    MeshedReports.delete_report (data)

    return jsonify ({"status": "success"}), 200

@app.route ('/reports', methods=['PUT'])
@login_required
def reports_read_report ():
    data = request.get_data (as_text=True)

    MeshedReports.handle_report (data)

    return jsonify ({"status": "success"}), 200

#endregion

#region Server Settings Functions
def get_game_server_config (server):
    server_config = get_game_server_config_paths (server)

    config = configparser.ConfigParser()
    config.read (server_config)

    return config

def get_game_server_config_from_path (server_path):
    config = configparser.ConfigParser()
    config.read (server_path)

    return config

def get_game_server_config_paths (server):
    path = get_server_config_paths (server)
    config = read_config (path)

    saved_path = config['General']['saved_path_dont_touch']
    shared_dir = config['General']['shared_install_dir']

    if shared_dir == "True":
        server_config = os.path.join (saved_path, 'Config', f"{server}.ini")
    else:
        server_config = os.path.join (saved_path, 'Config', 'ServerConfig.ini')

    return server_config

def get_management_settings (server):
    path = get_server_config_paths (server)
    config = read_config (path)
    config_dict = {}
    for section in config.sections():
        config_dict[section] = {}
        for key, value in config.items (section):
            config_dict[section][key] = value

    return config_dict

def get_players_settings (server):
    path = get_server_config_paths (server)
    config = read_config (path)
    saved_path = config['General']['saved_path_dont_touch']
    admin_list = []
    owner_list = []
    whitelist_list = []

    with open (os.path.join (saved_path, 'AdminIDs.ini'), 'r') as file:
        for line in file:
            admin_list.append (line.strip())
    
    with open (os.path.join (saved_path, 'OwnerIDs.ini'), 'r') as file:
        for line in file:
            owner_list.append (line.strip())
    
    with open (os.path.join (saved_path, 'WhitelistIDs.ini'), 'r') as file:
        for line in file:
            whitelist_list.append (line.strip())

    
    players_dict = {
        'admins': admin_list,
        'owners': owner_list,
        'whitelist': whitelist_list
    }

    return players_dict

def get_server_settings (server):
    config = get_game_server_config (server)
    config_dict = {}

    for option in config['/Game/SCPPandemic/Blueprints/GI_PandemicGameInstance.GI_PandemicGameInstance_C']:
        if option == 'GameplayConfig':
            continue

        config_dict[option] = config['/Game/SCPPandemic/Blueprints/GI_PandemicGameInstance.GI_PandemicGameInstance_C'][option]

    return config_dict

def get_gameplay_settings (server):
    config = get_game_server_config (server)

    gameplay_settings_raw = config['/Game/SCPPandemic/Blueprints/GI_PandemicGameInstance.GI_PandemicGameInstance_C']['GameplayConfig']
    gameplay_settings = gameplay_settings_raw.replace('(', '').replace(')', '').split(',')

    settings_dict = {}

    for setting in gameplay_settings:
        key, value = setting.split('=')
        settings_dict[key] = value

    return settings_dict

def get_server_config_paths (server):
    return get_server_from_name (server).config_path

def apply_management_settings (server, settings):
    try:
        path = get_server_config_paths (server)
        config = read_config (path)

        for key, value in settings.items():
            config.set ('General', key, str(value))

        with open(path, 'w') as configfile:
            config.write(configfile)

        return jsonify ({'status' : 'success'}), 200
    except Exception as e:
        return {"status": "error", "message": str(e)}, 500

def apply_players_settings (server, settings):
    try:
        path = get_server_config_paths (server)
        config = read_config (path)

        saved_path = config['General']['saved_path_dont_touch']

        admins = settings.get ('admins', [])
        owners = settings.get ('owners', [])
        whitelist = settings.get ('whitelist', [])

        with open (os.path.join (saved_path, 'AdminIDs.ini'), 'w') as file:
            for player in admins:
                file.write (player + '\n')

        with open (os.path.join (saved_path, 'OwnerIDs.ini'), 'w') as file:
            for player in owners:
                file.write (player + '\n')

        with open (os.path.join (saved_path, 'WhitelistIDs.ini'), 'w') as file:
            for player in whitelist:
                file.write (player + '\n')

        return jsonify ({'status' : 'success'}), 200
    except Exception as e:
        return {"status": "error", "message": str(e)}, 500

def apply_server_settings (server, settings):
    try:
        path = get_game_server_config_paths (server)
        config = get_game_server_config_from_path (path)

        data = settings

        for key, value in data.items():
            config.set ('/Game/SCPPandemic/Blueprints/GI_PandemicGameInstance.GI_PandemicGameInstance_C', key, str(value))
        
        with open(path, 'w') as configfile:
            config.write(configfile)
        
        return jsonify ({"status" : "success"}), 200
    except Exception as e:
        return {"status": "error", "message": str(e)}, 500

def apply_gameplay_settings (server, settings):
    try:
        path = get_game_server_config_paths (server)
        config = get_game_server_config_from_path (path)

        gameplay_config_str = config.get ('/Game/SCPPandemic/Blueprints/GI_PandemicGameInstance.GI_PandemicGameInstance_C', 'GameplayConfig')

        gameplay_config = parse_gameplay_config (gameplay_config_str)

        for key, value in settings.items():
            gameplay_config[key] = value

        new_gameplay_config = format_gameplay_config (gameplay_config)

        config.set ('/Game/SCPPandemic/Blueprints/GI_PandemicGameInstance.GI_PandemicGameInstance_C', 'GameplayConfig', new_gameplay_config)

        with open(path, 'w') as configfile:
            config.write(configfile)
        
        return jsonify ({"status" : "success"}), 200
    except Exception as e:
        return {"status": "error", "message": str(e)}, 500

#endregion

#region Misc Server Functions
def get_servers():
    with app.app_context():
        if 'servers' not in g:
            g.servers = app.config['servers']
        return g.servers

def get_reports():
    with app.app_context():
        if 'reports' not in g:
            g.reports = app.config['reports']
        return g.reports

def get_reports_per_user():
    with app.app_context():
        if 'reports_per_user' not in g:
            g.reports_per_user = app.config['reports_per_user']
        return g.reports_per_user
    
def set_servers():
    global server_info

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
            "server_status": info.server_status,
            "user_reports": get_reports_per_user()
        }
        for info in server_info
    ]

    try:
        with app.app_context():
            with get_lock():
                get_servers().clear()  # Clear existing server data

                # Populate the server information from the global variable
                for info in server_info_dicts:
                    server_name = info['server_name']
                    server_obj = ServerInfo(name=server_name)
                    server_obj.__dict__.update(info)  # Update the server object with the info
                    get_servers()[server_name] = server_obj
    except Exception as e:
        MeshedLogging.write_to_log_error (f"Error setting server info {e}", 40, method="set_servers()")

def set_reports(reports):
    with app.app_context():
        with get_lock():
            get_reports().clear()

            app.config['reports'] = reports

def set_reports_per_user(reports):
    with app.app_context():
        with get_lock():
            get_reports_per_user().clear()

            app.config['reports_per_user'] = reports

def get_encoded_servers():
    servers = get_servers()

    encoded_servers = {
        server_name: {
            'server_name_encoded': server.name.replace(' ', '_'), 
            'server_name': server.name,
            'current_users': server.current_users,
            'server_status': server.server_status,
            'gamemode_changes': server.gamemode_changes,
            'server_restarts': server.server_restarts,
            'current_game': server.current_game,
            'current_gamemode': server.current_gamemode,
            'previous_game': server.previous_game,
            'current_checkpoint': server.current_checkpoint,
            'last_completed_objective': server.last_completed_objective,
            'player_deaths': server.player_deaths,
            'game_attempts': server.game_attempts,
            "user_reports": get_reports_per_user()
        }
        for server_name, server in servers.items()
    }
    return encoded_servers

def get_logs(line_count=10, start_range=0, server=None):
    global app_name, app_author

    output_lines = None
    try:
        with open (os.path.join (platformdirs.user_log_dir(app_name, app_author, ensure_exists=True), "log.txt"), 'r') as logs:
            all_lines = logs.readlines()
            if server:
                all_the_server_logs = []
                for log in all_lines:
                    match = re.match(r'\[(.*?)\] (.*?) - (.*)', log)
                    match2 = re.match(r'\[(.*?)\] \[(\w*?)] (.*?) - (.*) (.*)', log)
                    if match2:
                        timestamp, severity, server_name, log_message, method = match2.groups()
                        if server_name == server:
                            all_the_server_logs.append (log)
                    elif match:
                        timestamp, server_name, log_message = match.groups()
                        if server_name == server:
                            all_the_server_logs.append(log)
                output_lines = all_the_server_logs
            else:
                output_lines = all_lines 
    except PermissionError as e:
        print ("Permission error accessing logs")
    except FileNotFoundError as e:
        print ("FileNotFound error accessing logs. Is the server manager running?")
    except Exception as e:
        print (e)
    
    if output_lines:
        if start_range >= len(output_lines):
            return []  
        
        output_lines.reverse()

        end_range = min(start_range + line_count, len(output_lines))

        return output_lines[start_range:end_range]
    else:
        return []

def read_log_pages (page_size=10):
    global app_name, app_author
    line_count = 0

    try:
        with open(os.path.join (platformdirs.user_log_dir(app_name, app_author, ensure_exists=True), "log.txt"), 'r') as file:
            line_count = sum(1 for _ in file)
    except PermissionError as e:
        print ("Permission error accessing logs")
    except FileNotFoundError as e:
        print ("FileNotFound error accessing logs")
    except Exception as e:
        print (e)

    return math.ceil (line_count / page_size)
    
def get_lock():
    return app.config['lock']

#endregion

#region Server Classes

class Server:
    def __init__(self, name, config, server_info):
        self.name = name
        self.server_info = server_info
        self.config_path = config

        self.server_process = None
        self.log = None
        self.analysis_thread = None
        self.server_started = False
        self.valid_dir_flag = False

        self.current_line = 0
        self.log_check_interval = int (read_global_config()['General']['log_checking_interval'])
        self.last_crash = None
        self.manual_kill_flag = False
        self.manual_shutdown_flag = False

        self.lock = threading.Lock()

    def create_server (self, shared_dir=False):
        self.server_info.server_status_change (-5)
        MeshedLogging.register_server_creating (self.name)
        self.read_server_config ()
        self.launch_server_dry (shared_dir)
        time.sleep (3)
        self.kill_server ()
        self.server_info.server_status_change (-3)
        MeshedLogging.register_server_created (self.name)

    def update_server_path_name (self, new_name):
        global data_dir

        self.name = new_name
        self.server_info.name = new_name
        self.config_path = os.path.join (data_dir, f"Server_{new_name}", "config.ini")
        self.read_server_config ()

    def init_server (self):
        self.read_server_config ()

        if self.shared_dir:
            MeshedReports.register_reports_directory (f"Shared dir: {self.name}", os.path.join (self.saved_file_path, 'Reports'))
    
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
        
        self.check_if_valid_install_dir()
        
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
            MeshedLogging.write_to_log_error ("Could not detect operating system", 50, self.name, "update_file_paths ()")
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
                self.valid_dir_flag = True
            else:
                self.valid_dir_flag = False
        else:
            self.valid_dir_flag = False

    def start_log_analysis (self):
        time.sleep (2)
        MeshedLogging.write_to_log_error ("Starting log analysis", 10, self.name, "start_log_analysis()")
        if self.analysis_thread != None:
            MeshedLogging.write_to_log_error ("Trying to start log analysis whilst a thread already exists.", 30, self.name, "start_log_analysis()")

        self.analysis_thread = threading.Thread (target=self.analyze_log, daemon=True)
        self.analysis_thread.start()

    def start_server (self):
        if not self.valid_dir_flag:
            MeshedLogging.write_to_log_error ("Attempted to start server with invalid server installation path. Please update the path to the proper root of the SCP Pandemic Server install.", 40, self.name)
            return

        if not self.check_if_install_initialized_settings ():
            MeshedLogging.write_to_log_error ("Server not successfully created. Trying to create it again.", 30, self.name, "start_server()")
            self.create_server()
            return

        self.server_info.server_status_change (2)
        MeshedLogging.register_server_start (self.name)
        self.read_server_config()
        update_server_path_name (self.name) 
        self.init_motd ()
        self.reset_vars()
        self.launch_server()
        
    def check_if_install_initialized_settings (self):
        if os.path.exists (os.path.join (self.saved_file_path, "Config", "ServerConfig.ini")):
            return True
        else:
            return False
            
    def launch_server (self):
        if not self.server_process:  
            MeshedLogging.write_to_log_error ("Launching server", 10, self.name, "launch_server()")
            update_server_banlists()

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
        else:
            MeshedLogging.write_to_log_error ("Launching server whilst an active server process is already open", 10, self.name, "launch_server()")

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
        self.shutdown_server()

    def wake_server (self):
        self.server_info.server_status_change (1)
        MeshedLogging.register_server_wake (self.name)
        time.sleep (3)
        self.init_server()

    def active_server (self):
        if self.server_info.server_status != 5:
            self.server_info.server_status_change (5)
            MeshedLogging.register_server_active (self.name)

    def stop_server(self):
        self.server_info.server_status_change (0)
        MeshedLogging.register_server_stop (self.name)
        time.sleep (2)
        if self.server_process:
            self.kill_server()
    
    def shutdown_server(self):
        self.manual_kill_flag = True
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
                MeshedLogging.register_server_offline (self.name)
            except psutil.NoSuchProcess:
                pass
            finally:
                self.server_process = None

    def suspend_server(self):
        self.shutdown_server()
        self.server_info.server_status_change (-1)
        MeshedLogging.register_server_suspend (self.name)

        while True:
            if self.is_active_hours (self.start_time, self.end_time):
                break

            time.sleep (60)
        
        self.wake_server()

    def restart_server(self, reason):
        self.server_info.server_status_change (3)
        MeshedLogging.register_server_restart (self.name, reason)
        if not reason:
            self.init_motd()

        time.sleep (0.5)
        self.shutdown_server()
        time.sleep (2)
        self.start_server()
        
    def idle_server (self):
        self.server_info.server_status_change (4)
        MeshedLogging.register_server_idle (self.name)

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
        self.manual_kill_flag = False
        self.manual_shutdown_flag = False

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
                # Setup the log file for reading
                try:
                    self.log = pygtail.Pygtail(self.log_file_path)
                except FileNotFoundError:
                    MeshedLogging.write_to_log_error (f"Log file {self.log_file_path} not found.", 40, self.name)
                    return
                except Exception as e:
                    MeshedLogging.write_to_log_error (f"Unexpected exception while opening log. {e}", 40, self.name)

                
                # Keep executing while the server is active.
                # Server will be labeled inactive if the server restarts, suspends or crashes.
                server_active = True
                while server_active and not self.manual_kill_flag:

                    # Check if the server has crashed
                    if not self.manual_shutdown_flag:
                        if self.server_process != None:
                            if self.server_process.poll() != None:
                                server_active = False
                                self.server_crashed()
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
                            objective_completed = MeshedRegex.log_is_objective_completed (line)
                            if objective_completed:
                                self.server_info.objective_completed (objective_completed)

                            # Has a checkpoint been reached? 
                            checkpoint = MeshedRegex.log_is_new_checkpoint (line)
                            if checkpoint:
                                self.server_info.new_checkpoint (checkpoint)

                            # Has a player died?
                            player_death = MeshedRegex.log_has_player_died (line)
                            if player_death:
                                self.server_info.player_died ()

                            # Has the game ended?
                            game_ended = MeshedRegex.log_has_game_ended (line)
                            if game_ended:
                                self.server_info.game_ended ()

                            # Has the game started?
                            game_started = MeshedRegex.log_is_game_started (line)
                            if game_started:
                                self.server_info.game_started ()

                            # Is the next game declared?
                            # next_game = log_is_next_game (line)

                            # Is the next game loading?
                            game_loading = MeshedRegex.log_is_game_loading (line)
                            if game_loading:
                                self.server_info.game_loading (game_loading)
                                
                                if self.manual_shutdown_flag:
                                    server_active = False
                                    server_logging = False
                                    self.shutdown_server()
                                    break

                                if self.active_hours:
                                    if not self.is_active_hours ():
                                        server_active = False
                                        server_logging = False
                                        self.suspend_server()
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
                            gamemode = MeshedRegex.log_is_new_gamemode (line)
                            if gamemode:
                                self.server_info.new_gamemode (gamemode)

                                delimited_string = self.restricted_gamemode.split('?')
                                if len (delimited_string) > 1:
                                    if self.server_info.current_game != delimited_string[0] or self.server_info.current_gamemode != delimited_string[1]:
                                        self.restart_server(f"Server loaded a gamemode that is not {self.restricted_gamemode}")
                                        server_active = False
                                        break

                            # Has session been created?
                            session_create = MeshedRegex.log_is_session_creation (line)
                            if session_create:
                                self.server_info.session_created ()

                            # Is server idling?
                            server_idle = MeshedRegex.log_is_entering_idle (line)
                            if server_idle:
                                self.idle_server()

                            # Is the latest log a player joining? Log it in the server info.
                            player_name, player_hex = MeshedRegex.log_is_player_joined(line)
                            if player_hex:
                                player_id = MeshedRegex.log_get_steam_id_from_hex (player_hex)
                                if player_id not in self.server_info.current_users:
                                    self.server_info.player_join(player_id, player_name)

                            # Is the latest log a player leaving? Log it in the server info.
                            player_id = MeshedRegex.log_is_player_leave(line)
                            if player_id:
                                if player_id in self.server_info.current_users:
                                    self.server_info.player_leave(player_id)
  
                            # Is this the first time the server has started? Init the server.
                            if not self.server_started:
                                session_create = MeshedRegex.log_is_session_creation (line)

                                if session_create:
                                    self.server_info.server_status_change (4)
                                    self.idle_server()
                                    self.server_started = True
                                
                    time.sleep(self.log_check_interval)
        MeshedLogging.write_to_log_error ("Ending analysis thread", 10, self.name, "analyze_log()")
        self.analysis_thread = None


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
        MeshedLogging.register_player_join (self.name, player, player_name)
    
    def player_leave (self, player):
        self.total_user_disconnects += 1
        self.disconnected_users.add(player)
        player_name = self.current_users[player]
        del self.current_users[player]

        MeshedLogging.register_player_leave (self.name, player, player_name)
        self.check_if_server_empty()

    def check_if_server_empty (self):
        if len (self.current_users) == 0:
            self.server_empty()

    def server_empty (self):
        self.idle_time = datetime.now()
        MeshedLogging.register_server_empty (self.name)

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
        MeshedLogging.register_checkpoint (self.name, checkpoint)

    def objective_completed (self, objective):
        self.last_completed_objective = objective
        MeshedLogging.register_objective_completed (self.name, objective)

    def player_died (self):
        self.player_deaths += 1
        MeshedLogging.register_player_died (self.name)

    def game_ended (self):
        self.server_status_change (6)
        MeshedLogging.register_game_ended (self.name)

    def game_started (self):
        self.server_status_change (5)
        MeshedLogging.register_game_started (self.name)
    
    def game_loading (self, game):
        self.game_change (game)
        self.server_status_change (7)
        MeshedLogging.register_game_loading (self.name, game)

    def new_gamemode (self, gamemode):
        self.current_gamemode = gamemode
        MeshedLogging.register_gamemode_loading (self.name, gamemode)
    
    def session_created (self):
        self.server_status_change (4)
        MeshedLogging.register_session_created (self.name)

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

        set_servers()
    
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
            MeshedLogging.write_to_log_error (e, method="update_server_path_name()")

        server_instance.update_server_path_name (server_new_name)

#endregion

#region Server Commanding

def server_command_execute_server_start (server):
    get_server_from_name (server).execute_server_start()

def server_command_execute_server_restart (server):
    get_server_from_name (server).execute_server_restart ()
    
def server_command_execute_server_stop (server):
    get_server_from_name (server).execute_server_stop ()

def server_command_execute_server_kill (server):
    get_server_from_name (server).execute_server_kill ()

#endregion

#region Server creation

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
    
    MeshedLogging.register_server_created (server_name)
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

#endregion

#region Banlisting

def add_user_to_global_ban_list (user_id):
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

#endregion

#region Server IO Stuff

def get_server_from_name (name):
    global servers

    for server in servers:
        if (server.name == name):
            return server
    
    return None

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

def get_global_config ():
    config_dir = platformdirs.user_config_dir ("Meshed Server Tool", "Skomesh")

    config_file = os.path.join (config_dir, "config.ini")

    if not os.path.exists (config_file):
        generate_global_config ()
    
    config = configparser.ConfigParser()
    config.read (config_file)

    return config

def read_global_config ():
    global web_server_port

    config = get_global_config ()

    web_server_port = config['WebServer']['web_server_port']

    return config
    
def generate_global_config ():
    global config_dir

    config_file = os.path.join (config_dir, "config.ini")

    new_config = configparser.ConfigParser()
    new_config['WebServer'] = {
        'web_server_port': 5000
    }
    new_config['General'] = {
        'log_checking_interval': 4,
        'debug_logging_level': 30
    }
    new_config['MOTD'] = {
        'global_server_motd': ''
    }
    with open (config_file, 'w') as config:
        new_config.write (config)

#endregion

#region Main

def main ():

    init_set_user_dirs()    

    init_logging()

    init_all_servers ()

    init_report_checking_thread()

    init_ban_lists()

    init_web_server()

def init_set_user_dirs ():
    global data_dir, config_dir, app_name, app_author

    data_dir = platformdirs.user_data_dir (app_name, app_author, ensure_exists=True)
    config_dir = platformdirs.user_config_dir (app_name, app_author, ensure_exists=True)

def init_logging ():
    MeshedLogging.create_log_file()
    logging.basicConfig(level=logging.ERROR)
    logger = logging.getLogger('waitress')
    logger.setLevel (logging.ERROR)

def init_all_servers ():
    configs = get_all_server_paths()

    for config in configs:
        folder_split = config['folder'].split ('_')
        name = folder_split[1]
        begin_server (name)
    
    set_servers()

def init_report_checking_thread():
    global report_thread

    if report_thread is None:
        report_thread = threading.Thread(target=report_checking_thread, daemon=True).start()

def report_checking_thread ():
    while True:
        reports, reports_per_user = MeshedReports.search_directories ()
        set_reports (reports)
        set_reports_per_user (reports_per_user)
        time.sleep (10)

def init_web_server ():
    web_server_port = get_global_config ()['WebServer']['web_server_port']
    waitress.serve (app, listen=f"0.0.0.0:{web_server_port}", threads=8)

def init_ban_lists ():
    update_server_banlists()

if __name__ == '__main__':
    main()

#endregion
