# Mesh5kServerTool
    # Mesh Server Tool

    Author: Skomesh
    Version 2.0.0 (Alpha 2)

    Meshed Server Tool is a locally hosted web interface for managing SCP: 5K dedicated servers.

    ## Features

    - Create and configure servers through a web interface
    - Logs relevant details of all servers through one interface
    - Shutdown or restart servers through the web interface

    ## Python Dependencies

    - pygtail
    - requests
    - psutil
    - flask
    - flask-cors
    - flask-bootstrap
    - gunicorn
    - platformdirs

    To install dependencies, use cmd or terminal and execute the command: 
    pip install -r requirements.txt
	
	## Usage
    # On Linux
	Execute the LaunchManager.sh script from inside the LaunchScripts directory.
	./LaunchManager.sh
	To start the web server, execute the LaunchWebServer.sh script.
	
	# On Windows
	Run the LaunchManager.bat file.
	To run the web server, run the LaunchWebServer.bat file.