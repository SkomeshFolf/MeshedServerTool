from flask import Flask, request, render_template, jsonify
import json
import os
import configparser
from MeshServer import ServerInfo

app = Flask(__name__)

server_info = []

@app.route('/update_server_info', methods=['POST'])
def update_server_info():
    global server_info

    server_info = []
    try:
        data = request.json
        server_info_data = json.loads(data['server_info'])

        for info in server_info_data:
            server_name = info['server_name']
            server_obj = ServerInfo(name=server_name)
            server_obj.__dict__.update(info)
            server_info.append(server_obj)

        return "ServerInfo updated successfully"
    except Exception as e:
        print(f"Error updating server info: {e}")
        return 'Error updating server info', 500

@app.route('/')
def web_server_home ():
    global server_info
    return render_template('index.html', servers=server_info)

@app.route('/server/<server_name>')
def web_server_server_page(server_name):
    global server_info

    # Find the server with the matching name in the list
    matching_servers = [server for server in server_info if server.server_name == server_name]

    if matching_servers:
        # Use the first matching server (assuming server names are unique)
        return render_template('server.html', server=matching_servers[0])
    else:
        print("Server not found")
        return "Server not found", 404

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