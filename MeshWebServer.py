from flask import Flask, request, Response, render_template, jsonify, g, redirect, url_for
from flask_login import LoginManager, UserMixin, login_user, login_required, logout_user, current_user
from flask_cors import CORS
import json
from json.decoder import JSONDecodeError
import os
import configparser
import threading
import traceback
import socket
import secrets
import math
import re
import time
import MeshServer
from MeshServer import ServerInfo, UserReport
import platformdirs
import logging
import platform
import waitress

app_name = "Meshed Server Tool"
app_author = "Skomesh"

data_dir = platformdirs.user_data_dir (app_name, app_author, ensure_exists=True)
secret_key_file = os.path.join (data_dir, 'secret_key')

if not os.path.exists (secret_key_file):
    with open (secret_key_file, 'w') as file:
        file.write (secrets.token_hex (32))

with open (secret_key_file, 'r') as file:
    secret_key = file.read().strip()

app = Flask(__name__)
CORS(app)

app.config['servers'] = {}
app.config['new_reports'] = []
app.config['lock'] = threading.Lock ()
app.secret_key = secret_key

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
print (users)

class User(UserMixin):
    def __init__ (self, username):
        self.id = username

@login_manager.user_loader
def load_user (user_id):
    global users
    if user_id in users:
        return User (user_id)
    return None

def get_servers():
    with app.app_context():
        if 'servers' not in g:
            g.servers = app.config['servers']
        return g.servers

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
                    if match:
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


@app.route('/update_server_info', methods=['POST'])
def update_server_info():
    if not request.data:
        return jsonify({'status': 'failed', 'message': 'Empty data received'}), 204
    try:
        data = request.json

        if 'server_info' not in data:
            raise ValueError('Missing "server_info" key in JSON data')
        
        server_info_data = json.loads(data['server_info'])     

        with app.app_context():
            with get_lock():
                get_servers().clear()

                for info in server_info_data:
                    server_name = info['server_name']
                    server_obj = ServerInfo(name=server_name)
                    server_obj.__dict__.update(info)
                    get_servers()[server_name] = server_obj
            
            response_data = {'servers': []}

            with get_lock():
                for server_name, server in get_servers().items():
                    server_data = {
                        'server_name': server.server_name,
                        'current_users': len(server.current_users),
                        'server_status': server.server_status
                    }
                    response_data['servers'].append(server_data)

        return jsonify(response_data)
    except JSONDecodeError as e:
        return jsonify({'status': 'error', 'message': 'Invalid JSON data received'}), 400
    except Exception as e:
            print(f"Error updating server info: {e}, {data}")
            return 'Error updating server info', 500

@app.route('/receive_new_reports', methods=['POST'])
def receive_new_reports():
    if not request.data:
        return jsonify({'status': 'failed', 'message': 'Empty data received'}), 204
    
    try:
        data = request.get_json()
        reports_data = json.loads (data)

        lock = get_lock ()
        with lock:
            app.config['new_reports'].clear()

            for report in reports_data:
                app.config['new_reports'].append (report)

        return 'Sucess', 200

    except JSONDecodeError as e:
        return jsonify({'status': 'error', 'message': 'Invalid JSON data received'}), 400
    except Exception as e:
            print(f"Error receiving report info: {e}, {data}")
            return 'Error receiving report info', 500

@app.route('/stream_server_info')
@login_required
def stream_server_info():
    def generate():
        with app.app_context():
            while True:
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
                        'game_attempts': server.game_attempts
                    }
                    for server_name, server in server_info.items()
                }
                yield f"data: {json.dumps(server_info_dicts)}\n\n"

                time.sleep(1)

    return Response(generate(), mimetype='text/event-stream')

@app.route('/stream_all_server_logs')
@login_required
def stream_all_server_logs():
    def generate():
        with app.app_context():
            while True:
                logs = get_logs()
                yield f"data: {json.dumps(logs)}\n\n"

                time.sleep (2)
    return Response(generate(), mimetype='text/event-stream')

@app.route('/server/<server_name>/stream_server_logs')
@login_required
def stream_server_logs(server_name):
    def generate():
        with app.app_context():
            while True:
                logs = get_logs(server=server_name)
                yield f"data: {json.dumps(logs)}\n\n"

                time.sleep (2)
    return Response(generate(), mimetype='text/event-stream')

@app.route ('/stream_new_reports_quantity')
@login_required
def stream_new_reports_quantity ():
    def generate():
        with app.app_context():
            while True:
                lock = get_lock()
                with lock:
                    yield f"data: {json.dumps(len (app.config['new_reports']))}\n\n"

                time.sleep (3)
    return Response (generate(), mimetype='text/event-stream')

@app.route ('/stream_new_reports')
@login_required
def stream_new_reports ():
    def generate():
        with app.app_context():
            while True:
                lock = get_lock()
                with lock:
                    yield f"data: {json.dumps(app.config['new_reports'])}\n\n"

                time.sleep (7)
    return Response (generate(), mimetype='text/event-stream')

@app.route('/')
@login_required
def web_server_home ():
    return render_template('index.html', servers=get_servers(), logs=get_logs())

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

@app.route ('/create_user', methods=['POST', 'GET'])
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
    return render_template('user_reports.html', reports=app.config['new_reports'])

@app.route('/server/<server_name>')
@login_required
def web_server_server_page(server_name):
    server_info = get_servers()

    # Find the server with the matching name in the list
    matching_servers = [server for server in server_info.values() if server.server_name == server_name]

    if matching_servers:
        # Use the first matching server (assuming server names are unique)
        return render_template('server.html', server=matching_servers[0])
    else:
        return render_template ('404_server.html')

@app.route ('/create_server')
@login_required
def web_server_create_server_page ():
    return render_template ('create_server.html')

@app.route ('/steamcmd_guide')
@login_required
def steamcmd_guide ():
    return render_template ('steamcmd_guide.html')

@app.route ('/logs/<page>')
@login_required
def web_server_logs_page (page=1):
    return render_template ('logs.html', page=page)

@app.route ('/logs/get_max_pages', methods=['POST'])
@login_required
def get_log_pages ():
    data = request.get_json ()
    page = data.get ("page_size")
    return jsonify (read_log_pages (page))

@app.route ('/logs/get_logs', methods=['POST'])
@login_required
def get_page_logs ():
    data = request.get_json()
    page = data.get ("page")
    page_size = data.get ("page_size")
    return jsonify (get_logs (line_count=page_size, start_range=((page - 1) * page_size)))

@app.route ('/control_server', methods=['POST'])
@login_required
def control_server ():
    action = request.form.get("action")
    server = request.form.get("server")
    response = send_server_control (action, server)
    return jsonify (response['message']), response['status']

@app.route('/server/<server_name>/request_management_settings', methods=['POST'])
@login_required
def request_management_settings(server_name):
    return jsonify(get_management_settings(server_name))

@app.route('/server/<server_name>/request_players_settings', methods=['POST'])
@login_required
def request_players_settings(server_name):
    return jsonify(get_players_settings(server_name))

@app.route('/server/<server_name>/request_server_settings', methods=['POST'])
@login_required
def request_server_settings(server_name):
    return jsonify(get_server_settings(server_name))

@app.route('/server/<server_name>/request_gameplay_settings', methods=['POST'])
@login_required
def request_gameplay_settings(server_name):
    return jsonify(get_gameplay_settings(server_name))
    
@app.route('/server/<server_name>/submit_management_settings', methods=['POST'])
@login_required
def submit_management_settings (server_name):
    try:
        result = apply_management_settings (server_name, request.get_json())
        return result
    except Exception as e:
        return jsonify({"status": "error", "message": str(e)}), 500

@app.route('/server/<server_name>/submit_players_settings', methods=['POST'])
@login_required
def submit_players_settings (server_name):
    try:
        result = apply_players_settings (server_name, request.get_json())
        return result
    except Exception as e:
        return jsonify({"status": "error", "message": str(e)}), 500

@app.route('/server/<server_name>/submit_server_settings', methods=['POST'])
@login_required
def submit_server_settings (server_name):
    try:
        result = apply_server_settings (server_name, request.get_json())
        return result
    except Exception as e:
        return jsonify({"status": "error", "message": str(e)}), 500

@app.route('/server/<server_name>/submit_gameplay_settings', methods=['POST'])
@login_required
def submit_gameplay_settings (server_name):
    try:
        result = apply_gameplay_settings (server_name, request.get_json())
        return result
    except Exception as e:
        return jsonify({"status": "error", "message": str(e)}), 500

@app.route ('/submit_new_server', methods=['POST'])
@login_required
def submit_new_server():
    settings = request.get_json()

    action = "create"
    server = settings.get ("server_name")
    formdata = settings

    response = send_server_control (action, server, formdata=settings)
    return jsonify (response['message']), response['status']
    
@app.route ('/reports/ban', methods=['POST'])
@login_required
def reports_ban_user():
    try: 
        data = request.get_data(as_text=True)
        response = send_server_control ("ban", None, user_id=data)
        return jsonify (response['message']), response['status']

    except Exception as e:
        return jsonify ({"status": "error", "message": str(traceback.format_exc())}), 500

@app.route ('/reports/delete', methods=['POST'])
@login_required
def reports_delete_report ():
    try: 
        data = request.get_data (as_text=True)
        response = send_server_control ("delete_report", None, hash=data)
        return jsonify (response['message']), response['status']
    except Exception as e:
        return jsonify ({"status": "error", "message": str(traceback.format_exc())}), 500

@app.route ('/reports/read', methods=['POST'])
@login_required
def reports_read_report ():
    try: 
        data = request.get_data (as_text=True)
        response = send_server_control ("read_report", None, hash=data)
        lock = get_lock ()
        with lock:
            app.config['new_reports'] = [obj for obj in app.config['new_reports'] if obj['hash'] != data]
        return jsonify (response['message']), response['status']
    except Exception as e:
        return jsonify ({"status": "error", "message": str(traceback.format_exc())}), 500

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
    config = MeshServer.read_config (path)

    saved_path = config['General']['saved_path_dont_touch']
    shared_dir = config['General']['shared_install_dir']

    if shared_dir == "True":
        server_config = os.path.join (saved_path, 'Config', f"{server}.ini")
    else:
        server_config = os.path.join (saved_path, 'Config', 'ServerConfig.ini')

    return server_config

def get_management_settings (server):
    path = get_server_config_paths (server)
    config = MeshServer.read_config (path)
    config_dict = {}
    for section in config.sections():
        config_dict[section] = {}
        for key, value in config.items (section):
            config_dict[section][key] = value

    return config_dict

def get_players_settings (server):
    path = get_server_config_paths (server)
    config = MeshServer.read_config (path)
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
    action = 'get_server_config'
    server = server

    response = send_server_control (action, server)
    
    response_data = json.loads(response['response'].decode('utf-8'))
    
    return response_data

def apply_management_settings (server, settings):
    try:
        path = get_server_config_paths (server)
        config = MeshServer.read_config (path)

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
        config = MeshServer.read_config (path)

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

        gameplay_config = MeshServer.parse_gameplay_config (gameplay_config_str)

        for key, value in settings.items():
            gameplay_config[key] = value

        new_gameplay_config = MeshServer.format_gameplay_config (gameplay_config)

        config.set ('/Game/SCPPandemic/Blueprints/GI_PandemicGameInstance.GI_PandemicGameInstance_C', 'GameplayConfig', new_gameplay_config)

        with open(path, 'w') as configfile:
            config.write(configfile)
        
        return jsonify ({"status" : "success"}), 200
    except Exception as e:
        return {"status": "error", "message": str(e)}, 500


def send_server_control (action, server=None, **kwargs):
    
    payload = {
        "action": action,
        "server": server
    }

    payload.update (kwargs)

    payload_json = json.dumps (payload)
    
    if len(payload_json) > 4096:  # Adjust buffer size as needed
            raise ValueError("Data too large to send")

    server_socket = ('127.0.0.1', int (MeshServer.get_global_config()['WebServer']['web_server_port']) + 1)

    try:
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as client_socket:
            client_socket.connect(server_socket)
            client_socket.sendall(payload_json.encode ('utf-8'))
            response = b""
            while True:
                part = client_socket.recv(1024)
                if not part:
                    break
                response += part
            return {"status": 200, "message": "Success", "response": response}
    except socket.error as e:
        return {"status": 500, "message": f"Socket error: {e}"}
    except Exception as e:
        return {"status": 500, "message": f"Unexpected error {e}"}

def read_global_config ():
    if os.path.exists ("config.ini"):    
        config = configparser.ConfigParser()
        config.read ("config.ini")
        return config
    else:
        return None

def main ():
    os_name = platform.system ()
    #if os_name == "Windows":
    logging.basicConfig(level=logging.ERROR)
    logger = logging.getLogger('waitress')
    logger.setLevel (logging.ERROR)
    waitress.serve (app, listen='0.0.0.0:5000', threads=8)
    #else:
    #    app.run(debug=False, host='127.0.0.1')

if __name__ == '__main__':
    main()