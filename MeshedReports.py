import hashlib
import logging
import os
import time
import threading
import MeshedLogging
import chardet
import platformdirs

class UserReport:
    def __init__ (self, server, target, target_id, source, source_id, date, reason, text):
        self.server = server
        self.target = target
        self.target_id = target_id
        self.source = source
        self.source_id = source_id
        self.date = date
        self.reason = reason
        self.text = text
        self.hash = generate_hash(target, target_id, source, source_id, date, reason, text)
    
    def to_dict (self):
        return {
            'server': self.server,
            'target': self.target,
            'target_id': self.target_id,
            'source': self.source,
            'source_id': self.source_id,
            'date': self.date,
            'reason': self.reason,
            'text': self.text,
            'hash': self.hash
        }

    def __str__ (self):
        return f"Report(server={self.server}, target={self.target}, target_id={self.target_id}, source={self.source}, source_id={self.source_id}, date={self.date}, reason={self.reason}, text={self.text}, hash={self.hash})"
    
    def __eq__ (self, other):
        if isinstance(other, UserReport):
            return self.hash == other.hash
        return False

# Format: Array of dictionaries: {server: "name", dir: "path"}
report_directories = []



def generate_hash (target, target_id, source, source_id, date, reason, text):
    hasher = hashlib.md5()
    hasher.update(f"{target}{target_id}{source}{source_id}{date}{reason}{text}".encode('utf-8'))
    return hasher.hexdigest()
    
def register_reports_directory (server, dir):
    global report_directories

    dir_exists = False
    for directory in report_directories:
        if directory['dir'] == dir:
            dir_exists = True
            break

    if dir_exists:
        MeshedLogging.write_to_log_error (f"{dir} is already a registered report directory.", 10, method="MeshedReports.register_reports_directory()")
    else:
        report_directories.append({"server": server, "dir": dir})

        if not os.path.exists (dir):
            os.makedirs (dir)
            MeshedLogging.write_to_log_error (f"{dir} doesn't exist. Creating..", 20, method="MeshedReports.register_reports_directory()")

def remove_reports_directory (dir):
    global report_directories

    for directory in report_directories:
        if directory['dir'] == dir:
            report_directories.remove (directory)

def search_directories():
    all_reports = []
    handled_reports = []
    new_reports = []
    reports_per_user = {}

    for directory in report_directories:
        dir = directory['dir']
        server = directory['server']

        if not os.path.isdir(dir):
            MeshedLogging.write_to_log_error (f"Report directory '{dir}' does not exist.", 20, method="MeshedReports.search_directories()")
            remove_reports_directory(dir)
            continue

        for filename in os.listdir(dir):
            file_path = os.path.join(dir, filename)
            if os.path.isfile(file_path):
                with open(file_path, 'rb') as file:
                    raw_data = file.read()

                result = chardet.detect(raw_data)
                encoding = result['encoding']

                try:
                    with open(file_path, 'r', encoding=encoding) as file:
                        file_contents = file.read()
                except UnicodeDecodeError:
                    MeshedLogging.write_to_log_error (f"Could not decode file '{file_path}'. Attempting to read with errors='replace'.", 20, method="MeshedReports.search_directories()")
                    with open(file_path, 'r', encoding=encoding, errors='replace') as file:
                        file_contents = file.read()

                report = parse_report(server, file_contents)

                if has_report_been_handled(report.hash):
                    if report not in handled_reports:
                        handled_reports.append (report.to_dict())
                else:
                    if report not in new_reports:
                        new_reports.append(report.to_dict())
                
                if report not in all_reports:
                    all_reports.append (report.to_dict())
    
    reports_per_user = generate_dictionary_of_reported_users(all_reports)

    return new_reports, reports_per_user
            
def has_report_been_handled (hash):
    data_dir = get_data_dir()
    handled_path = os.path.join (data_dir, "handled_reports.txt")

    if not os.path.isfile(handled_path):
        with open (handled_path, 'w') as file:
            pass

    with open(handled_path, 'r') as file:
        for line in file:
            if line.strip() == hash:
                return True
    
    return False

def parse_report(server, file_contents):
    # Split the lines of the file
    lines = file_contents.split('\n')

    # Parse individual fields based on line number
    target_id, target = lines[0].split(',')
    source_id, source = lines[1].split(',')
    date_str = lines[2].strip()
    date = date_str
    reason = lines[4].strip()
    text = lines[5].strip()

    return UserReport(server, target, target_id, source, source_id, date, reason, text)

def handle_report (hash):
    data_dir = get_data_dir()
    handled_path = os.path.join (data_dir, "handled_reports.txt")

    with open (handled_path, 'a') as file:
        file.write (hash + '\n')

def generate_dictionary_of_reported_users (all_reports):
    reports_per_user = {}

    for report in all_reports:
        target = report['target_id']
        if target in reports_per_user:
            reports_per_user[target] += 1
        else:
            reports_per_user[target] = 1
    
    return reports_per_user

def get_reported_users ():
    global reports_per_user

    return reports_per_user

def get_new_reports ():
    global new_reports

    return new_reports

def delete_report (hash):
    raise NotImplementedError

def get_data_dir ():
    app_name = "Meshed Server Tool"
    app_author = "Skomesh"

    data_dir = platformdirs.user_data_dir (app_name, app_author, ensure_exists=True)

    return data_dir