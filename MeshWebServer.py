from flask import Flask, request, render_template, jsonify, g
import json
import os
import configparser
import threading
from MeshServer import ServerInfo

app = Flask(__name__)

app.config['servers'] = {}
app.config['lock'] = threading.Lock()

def get_servers():
    if 'servers' not in g:
        g.servers = app.config['servers']
    return g.servers

def get_lock():
    return app.config['lock']

@app.route('/update_server_info', methods=['POST'])
def update_server_info():
    try:
        data = request.json

        if 'server_info' not in data:
            raise ValueError ('Missing "server_info" key in JSON data')
        
        server_info_data = json.loads(data['server_info'])     

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
    except Exception as e:
        print(f"Error updating server info: {e}, {data}")
        return 'Error updating server info', 500

@app.route('/')
def web_server_home ():
    return render_template('index.html', servers=get_servers())

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

@app.route ('/control_server', methods['POST'])
def control_server ():
    action = request.form.get('action')
    

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
    #port = int(config['WebServer']['port'])
    app.run()

if __name__ == '__main__':
    main()