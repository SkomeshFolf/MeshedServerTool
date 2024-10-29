# MeshedServerTool
    # Meshed Server Tool

    Author: Skomesh
    Version 2.0.2

    Meshed Server Tool is a locally hosted web interface and server manager SCP: 5k or SCP Pandemic Dedicated Servers.

    ## Features

        - Create and configure servers through a web interface
        - Logs relevant details of all servers through one interface
        - Manage servers through the web interface
        - In-game reports all visible through the web interface with easy banning across all servers


    ## What's Missing (What this program doesn't do)

        - In-game chat monitoring, this is a limitation of the game itself
        - In-game moderation; bans will only take place upon a server restart, kicking is impossible, ect. This is a game limitation
        - HTTPS/TLS/SSL: Your webpage does not encrypt traffic, it only uses HTTP. To support HTTPS, this app would have to support reverse proxy.
                         I deemed an app of this scale to not require encryption, as setting up a reverse proxy server would put too much work
                         on the end user of this program (you).
    

    ## Required Software

        Python 3.12 or newer


    ## Python Dependencies

        To install dependencies, use cmd or terminal and execute the command: 
        pip install -r requirements.txt
        
        On Windows, ensure your command prompt is at the same directory as the file.
        You can do this by using the command: 
            cd /d <path>
                Example: cd /d C:/Users/User/Desktop/MeshedServerTool

	
	## Usage
        # On Linux
        Execute the launch.sh script
        ./launch.sh
        
        # On Windows
        Run the launch.bat file.

        # To access the web interface
        - If this is locally hosted on your machine, go to your web browser, and type in the URL: http://127.0.0.1:5000
        - If this is hosted on another machine on your local network, use the host's local IPv4, such as http://192.168.1.101:5000
        - If the host is outside of your local network, or you wish to access the web interface from anywhere:
            you must log into your router and set up port forwarding: port 5000 for TCP, IPv4 set to the host's machine.
            After the port is forwarded, you can access the web interface using your public ip address. Example: http://203.0.113.0:5000
            You can find this by googling "What is my ip?"
            You can learn more about port forwarding by searching on the internet.

        # First time setup
        When you enter the web interface for the first time, it will prompt you to make a user. These are the credentials needed to access the web interface.
        Be advised, due to the nature of HTTP, this data is NOT ENCRYPTED over the internet, therefore it is ill-advised to reuse a password used for something else.

        # Adding a server
        On the navbar, you can navigate to the "Add Server" page.
        YOU STILL NEED TO INSTALL A DEDICATED SERVER USING STEAMCMD. Instructions are in the warning at the top of the page.
        Next, you can configure the server settings.
        - Server Installation Directory
            Make sure this is set to the root folder of your SCP 5k server installation.
            ex. C:/SteamCMD/steamapps/common/SCP Pandemic Dedicated Server
        - Shared Install Directory
            The blue info box already states what it does; if you are using ONE server install for MULTIPLE servers, click that checkbox. If you don't it may break some functionality.
        - Note for server settings:
            Not all settings are documented, and not all settings actually do anything. You are free to explore the settings.

        # Servers
        - Settings Admins, Owners and Whitelist
            These settings usually do not save while the server is running. A server must be shutdown before changing the settings.
            The expected value are Steam ID 64. You can find Steam IDs by searching online for steamid.io

        # Server Manager Settings
        Meshed Server Tool saves all of its settings in appdata or .local directories. You can find them at:
        C:\Users\User\AppData\Local\Skomesh\Meshed Server Tool
        or
        /home/user/.local/share/Meshed Server Tool
        /home/user/.config/Meshed Server Tool
        

    ## TODO
        Fix server name changing
        Viewable global ban list
        Settings for logging speed
        Integrated server updater
        More reliable reports menu
        Show read reports, not just new ones
        Bug fixing, exception handling
        MOTD messages, global and server specific
        Documentation of server settings
        Improved web interface
        HTTPS
        Deleting servers
        Log history
        Raw server log view


    ## Changelog

        - 2.0.2
            Added player names to the server list instead of just Steam IDs
            Fixed server names with spaces not properly updating its status on the main page
            

        - 2.0.1
            Hotfix
            Properly fixed the "Restricted Gamemode" and "Restart after X" to actual work.

        - 2.0.0
            Fully integrated web interface.
            Everything.

            # Known bugs:
            Reports screen a bit wonky when quickly marking reports as read
