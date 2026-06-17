// Static help page: install instructions for SteamCMD + SCP: 5K.
//
// Content is intentionally imperative and short — the operator should
// be able to read this top-to-bottom and end up with a working
// install directory they can paste into MeshedServerTool's
// "Install directory" field.
//
// SCP: 5K dedicated server appid: 884110 (per the v2 server docs).
// (The task spec mentioned 1213210 as a placeholder; we ship the
// real one used elsewhere in this repo.)

export default function SteamCmdGuidePage() {
  return (
    <section className="dashboard narrow" style={{ maxWidth: "48rem" }}>
      <h2>Installing an SCP: 5K server with SteamCMD</h2>
      <p className="muted">
        Use SteamCMD to fetch the dedicated server files, then point
        MeshedServerTool at the install directory. Run SteamCMD on the
        same machine that will host the game servers.
      </p>

      <ol style={{ paddingLeft: "1.25rem", lineHeight: 1.55 }}>
        <li className="mb-3">
          <h4 style={{ margin: "0.5rem 0" }}>1. Install SteamCMD</h4>
          <p>
            Follow Valve's official instructions:{" "}
            <a
              href="https://developer.valvesoftware.com/wiki/SteamCMD#Downloading_SteamCMD"
              target="_blank"
              rel="noreferrer"
            >
              developer.valvesoftware.com/wiki/SteamCMD
            </a>.
          </p>
          <p className="muted">Common one-liners:</p>
          <pre className="log-pane" style={{ maxHeight: "none" }}>
{`# Linux (Debian/Ubuntu) — install as the 'steam' user
sudo apt update
sudo apt install -y lib32gcc-s1 lib32stdc++6 steamcmd

# Windows (PowerShell, as Administrator)
# Download steamcmd.zip from the link above and extract it somewhere
# stable such as C:\\SteamCMD, then add that folder to PATH.`}
          </pre>
        </li>

        <li className="mb-3">
          <h4 style={{ margin: "0.5rem 0" }}>2. Pick an install directory</h4>
          <p>
            Choose an empty folder you'll point MeshedServerTool at.
            Examples:
          </p>
          <ul>
            <li><code>C:\SteamCMD\steamapps\common\SCP Pandemic Dedicated Server</code></li>
            <li><code>/home/steam/Steam/scp5k</code></li>
          </ul>
          <p className="muted">
            You'll paste this exact path into the{" "}
            <strong>Install directory</strong> field when creating a
            server in MeshedServerTool.
          </p>
        </li>

        <li className="mb-3">
          <h4 style={{ margin: "0.5rem 0" }}>3. Install the dedicated server</h4>
          <p>
            From a shell, run this one-shot command (replace{" "}
            <code>&lt;install_dir&gt;</code> with your path from step 2):
          </p>
          <pre className="log-pane" style={{ maxHeight: "none" }}>
{`steamcmd +login anonymous \\
         +force_install_dir <install_dir> \\
         +app_update 884110 validate \\
         +quit`}
          </pre>
          <p className="muted">
            The <code>validate</code> flag checks existing files; drop it
            for faster re-runs when you only need to refresh missing
            files.
          </p>
          <p className="muted">
            To update later, just re-run the same command. SteamCMD will
            diff and patch in place.
          </p>
        </li>

        <li className="mb-3">
          <h4 style={{ margin: "0.5rem 0" }}>4. Register the server in MeshedServerTool</h4>
          <p>
            Open the dashboard, click <strong>Add server</strong>, and:
          </p>
          <ul>
            <li>Name: anything you like (e.g. <code>scp5k-main</code>)</li>
            <li>
              Install directory: the same <code>&lt;install_dir&gt;</code>{" "}
              you used above
            </li>
            <li>Executable: typically <code>SCPPandemicServer.exe</code></li>
          </ul>
          <p className="muted">
            Once registered, head to the server detail page and hit{" "}
            <strong>Start</strong>. Logs and chat will start streaming
            into the per-server and aggregate views.
          </p>
        </li>
      </ol>
    </section>
  );
}
