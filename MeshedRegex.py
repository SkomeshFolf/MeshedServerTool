import re

def log_is_objective_completed (line):
    match = re.search(r'LogObjectives: Completed Objective (.*?) successfully', line)
    return match.group(1) if match else None

def log_is_new_checkpoint (line):
    match = re.search (r'LogGameState: Unlocked Checkpoint (.*)', line)
    return match.group(1) if match else None

def log_has_player_died (line):
    match = re.search (r'LogBlueprintUserMessages: Player Died', line)
    return bool (match)

def log_has_game_ended (line):
    match = re.search (r'LogBlueprintUserMessages: Changing Game status to GS_PostGame', line)
    return bool (match)

def log_is_game_started (line):
    match = re.search (r'LogBlueprintUserMessages: Gamemode started', line)
    return bool (match)

def log_is_next_game (line):
    match = re.search(r'Map vote has concluded, travelling to (.+)', line)
    return match.group(1) if match else None

def log_is_game_loading (line):
    match = re.search (r'LogAIModule: Creating AISystem for world (.*)', line)

    if match:
        if match.group(1) == 'TransitionMap':
            return None
        else:
            return match.group (1)
    else:
        return None
    
def log_is_new_gamemode (line):
    match = re.search (r'LogLoad: Game class is \'(.*?)\'', line)
    return match.group(1) if match else None
    
def log_is_session_creation (line):
    match = re.search (r'Create session complete', line)
    return bool (match)

def log_is_entering_idle (line):
    match = re.search (r'Entering Standby, going to standby map M_ServerDefault.', line)
    return bool (match)

def log_is_player_joined (line):
    # old_match = re.search(r'Sending auth result to user (\d+)', line)
    match = re.search(r'\?Name=(.*?) userId:.*?\[?(0x[0-9A-Fa-f]+)\]', line)
    if match:
        return match.group(1), match.group (2)
    else:
        return None, None

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
    #for steam_id in steam_ids: 

def log_get_steam_id_from_hex (hex):
    return int(hex, 16)