from flask import Flask, request, Response, render_template, jsonify, g
from flask_cors import CORS
import json
from json.decoder import JSONDecodeError
import os
import configparser
import threading
import socket
import requests
import re
import time
import MeshServer
from MeshServer import ServerInfo

app = Flask(__name__)
CORS(app)

app.config['servers'] = {}
app.config['lock'] = threading.Lock()

def get_servers():
    with app.app_context():
        if 'servers' not in g:
            g.servers = app.config['servers']
        return g.servers

def get_logs(line_count=10, start_range=0, server=None):
    output_lines = None
    with open ('log.txt', 'r') as logs:
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
    
    if output_lines:
        output_lines.reverse()
        if start_range > len(output_lines):
            return output_lines[:line_count]
        else:
            return output_lines[start_range:start_range + line_count]

def get_lock():
    return app.config['lock']

def get_management_settings (server):
    path = get_server_config_paths (server)
    config = MeshServer.read_config (path)
    config_dict = {}
    for section in config.sections():
        config_dict[section] = {}
        for key, value in config.items (section):
            config_dict[section][key] = value
    return config_dict

def get_server_config_paths (server):
    action = 'get_server_config'
    server = server
    data = f"{server}:{action}"
    server_socket = ('127.0.0.1', int (MeshServer.read_global_config()['WebServer']['web_server_port']) + 1)
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as client_socket:
        client_socket.connect(server_socket)
        client_socket.send(data.encode ('utf-8'))

        response = b""
        while True:
            part = client_socket.recv(1024)
            if not part:
                break
            response += part
        
    # Decode the response and convert it to a Python dictionary
    response_data = json.loads(response.decode('utf-8'))
    
    return response_data

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

        return jsonify(response_data)  # Return JSON response directly
    except JSONDecodeError as e:
        return jsonify({'status': 'error', 'message': 'Invalid JSON data received'}), 400
    except Exception as e:
            print(f"Error updating server info: {e}, {data}")
            return 'Error updating server info', 500

@app.route('/stream_server_info')
def stream_server_info():
    def generate():
        with app.app_context():
            while True:
                server_info = get_servers()
                server_info_dicts = {
                    server_name: {
                        'server_name': server.server_name,
                        'current_users': server.current_users,
                        'server_status': server.server_status
                    }
                    for server_name, server in server_info.items()
                }
                yield f"data: {json.dumps(server_info_dicts)}\n\n"

                time.sleep(1)

    return Response(generate(), mimetype='text/event-stream')

@app.route('/stream_all_server_logs')
def stream_all_server_logs():
    def generate():
        with app.app_context():
            while True:
                logs = get_logs()
                yield f"data: {json.dumps(logs)}\n\n"

                time.sleep (2)
    return Response(generate(), mimetype='text/event-stream')

@app.route('/stream_server_logs')
def stream_server_logs():
    def generate():
        with app.app_context():
            while True:
                logs = get_logs()
                yield f"data: {json.dumps(logs)}\n\n"

                time.sleep (2)
    return Response(generate(), mimetype='text/event-stream')

@app.route('/')
def web_server_home ():
    return render_template('index.html', servers=get_servers(), logs=get_logs())

@app.route('/server/<server_name>')
def web_server_server_page(server_name):
    server_info = get_servers()

    # Find the server with the matching name in the list
    matching_servers = [server for server in server_info.values() if server.server_name == server_name]

    if matching_servers:
        # Use the first matching server (assuming server names are unique)
        return render_template('server.html', server=matching_servers[0])
    else:
        print("Server not found")
        return "Server not found", 404

@app.route ('/control_server', methods=['POST'])
def control_server ():
    action = request.form.get("action")
    server = request.form.get("server")
    data = f"{server}:{action}"
    server_socket = ('127.0.0.1', int (MeshServer.read_global_config()['WebServer']['web_server_port']) + 1)
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as client_socket:
        client_socket.connect(server_socket)
        client_socket.send(data.encode ('utf-8'))

    return "", 204

@app.route('/server/<server_name>/request_management_settings', methods=['POST'])
def request_management_settings(server_name):
    return jsonify(get_management_settings(server_name))
    
def read_global_config ():
    if os.path.exists ("config.ini"):    
        config = configparser.ConfigParser()
        config.read ("config.ini")
        return config
    else:
        return None

#@app.route('/user_reports')
#def web_server_user_reports():
#    user_reports_data = {'report1': 'User report 1', 'report2': 'User report 2'}
#    return render_template('user_reports.html', user_reports=user_reports_data)

def main ():
    config = read_global_config()
    #web_port = int(config['WebServer']['port'])
    app.run(debug=True, host='127.0.0.1')

if __name__ == '__main__':
    main()