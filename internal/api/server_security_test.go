package api

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestValidateInstallDir covers CRIT-2 — the install_dir allowlist.
// Table-driven over the rules in validateInstallDir.
func TestValidateInstallDir(t *testing.T) {
	t.Parallel()
	d := &v1ServerDeps{}
	d.SetInstallRoot("/opt/servers", nil, false)
	cases := []struct {
		name    string
		path    string
		wantErr bool
		errSub  string // substring expected in error
	}{
		{"empty", "", true, "required"},
		{"relative", "srv/foo", true, "absolute"},
		{"dotdot_in_clean", "/opt/servers/../etc/passwd", true, "under /opt/servers"},
		{"outside_root", "/home/skomesh/srv", true, "under /opt/servers"},
		{"root_itself", "/", true, "under /opt/servers"},
		{"etc", "/etc/foo", true, "under /opt/servers"},
		{"different_dir_same_prefix", "/opt/serversFoo", true, "under /opt/servers"},
		// Reserved system paths: only rejected at top level. A path like
		// /opt/servers/etc/foo is fine (it lives under install_root, not
		// under /etc). The real protection is the install_root check above.
		// When install_root is /opt/servers, /etc/* fails the under-root
		// check before the reserved check runs.
		{"etc_fails_under_root_check", "/etc/foo", true, "under /opt/servers"},
		{"opt_servers_with_etc_segment_ok", "/opt/servers/etc", false, ""},
		{"opt_servers_with_usr_bin_segment_ok", "/opt/servers/usr/bin", false, ""},
		{"valid_under_root", "/opt/servers/foo", false, ""},
		{"valid_under_root_nested", "/opt/servers/a/b/c", false, ""},
		{"root_with_trailing_slash", "/opt/servers", false, ""}, // exact root match is OK
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := d.validateInstallDir(tc.path)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("got nil error for %q, want error containing %q", tc.path, tc.errSub)
				}
				if tc.errSub != "" && !strings.Contains(err.Error(), tc.errSub) {
					t.Errorf("error %q does not contain %q", err.Error(), tc.errSub)
				}
			} else if err != nil {
				t.Errorf("got error %v for %q, want nil", err, tc.path)
			}
		})
	}
}

// TestValidateInstallDir_RootSlash verifies that --install-root="/" is
// permissive but still rejects reserved system paths (defense in depth).
func TestValidateInstallDir_RootSlash(t *testing.T) {
	t.Parallel()
	d := &v1ServerDeps{}
	d.SetInstallRoot("/", nil, false)
	if err := d.validateInstallDir("/tmp/srv"); err != nil {
		t.Errorf("/tmp/srv under root-slash should be accepted, got: %v", err)
	}
	if err := d.validateInstallDir("/etc/foo"); err == nil {
		t.Error("/etc/foo should be rejected (reserved path) even with root=/")
	}
	if err := d.validateInstallDir("/proc/cpuinfo"); err == nil {
		t.Error("/proc/* should be rejected (reserved path)")
	}
}

// TestValidateExecutablePath covers CRIT-1 — the executable allowlist.
func TestValidateExecutablePath(t *testing.T) {
	t.Parallel()
	d := &v1ServerDeps{}
	d.SetInstallRoot("/opt/servers", []string{"/opt/scpsl"}, false)
	cases := []struct {
		name       string
		exe        string
		installDir string
		wantErr    bool
	}{
		{"empty_ok", "", "/opt/servers/foo", false}, // unset is fine
		{"under_install_dir", "/opt/servers/foo/scpsl_server", "/opt/servers/foo", false},
		{"system_bin_sh", "/bin/sh", "/opt/servers/foo", false},
		{"system_usr_bin", "/usr/bin/python3", "/opt/servers/foo", false},
		{"system_local_bin", "/usr/local/bin/some-tool", "/opt/servers/foo", false},
		{"custom_allowed_root", "/opt/scpsl/scpsl_server", "/opt/servers/foo", false},
		{"under_install_dir_nested", "/opt/servers/foo/sub/scpsl_server", "/opt/servers/foo", false},
		{"arbitrary_path", "/usr/local/games/scp_server", "/opt/servers/foo", true},
		{"relative", "scpsl_server", "/opt/servers/foo", true},
		{"dotdot", "/opt/servers/../etc/passwd", "/opt/servers/foo", true},
		{"usr_lib_unexpected", "/usr/lib/foo", "/opt/servers/foo", true},
		{"etc_passwd", "/etc/passwd", "/opt/servers/foo", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := d.validateExecutablePath(tc.exe, tc.installDir)
			if tc.wantErr && err == nil {
				t.Errorf("got nil error for %q, want error", tc.exe)
			} else if !tc.wantErr && err != nil {
				t.Errorf("got error %v for %q, want nil", err, tc.exe)
			}
		})
	}
}

// TestValidateExecutablePath_AllowArbitrary flips on the dangerous flag
// and confirms anything goes (tests rely on this).
func TestValidateExecutablePath_AllowArbitrary(t *testing.T) {
	t.Parallel()
	d := &v1ServerDeps{}
	d.SetInstallRoot("/", nil, true) // tests' setup
	for _, exe := range []string{"/bin/sh", "/usr/bin/id", "/anything/at/all"} {
		if err := d.validateExecutablePath(exe, "/tmp"); err != nil {
			t.Errorf("allowArbitraryExe=true: %q should be accepted, got %v", exe, err)
		}
	}
}

// TestExecArgsFromBody_NonStringArgvDropped verifies the contract that
// non-string argv entries are silently dropped, matching buildCommand.
func TestExecArgsFromBody_NonStringArgvDropped(t *testing.T) {
	t.Parallel()
	d := &v1ServerDeps{}
	d.SetInstallRoot("/", nil, true)
	args := map[string]any{
		"executable": "/bin/sh",
		"argv":       []any{"-c", 42, "echo hi", nil, true},
	}
	_, argv, err := d.execArgsFromBody(args, "")
	if err != nil {
		t.Fatalf("execArgsFromBody: %v", err)
	}
	if len(argv) != 2 || argv[0] != "-c" || argv[1] != "echo hi" {
		t.Errorf("argv = %v, want [-c, echo hi]", argv)
	}
}

// TestExecArgsFromBody_RejectsBadExecutable verifies the validation
// error path is returned to the caller.
func TestExecArgsFromBody_RejectsBadExecutable(t *testing.T) {
	t.Parallel()
	d := &v1ServerDeps{}
	d.SetInstallRoot("/opt/servers", nil, false)
	args := map[string]any{"executable": "/usr/local/games/secret"}
	_, _, err := d.execArgsFromBody(args, "/opt/servers/foo")
	if err == nil {
		t.Fatal("expected error for arbitrary executable, got nil")
	}
	if !strings.Contains(err.Error(), "executable") {
		t.Errorf("error %q should mention 'executable'", err.Error())
	}
}

// TestInstallRoot_TempDirRelativePath ensures tests using t.TempDir()
// continue to work because TempDir returns an absolute path.
func TestInstallRoot_TempDirRelativePath(t *testing.T) {
	t.Parallel()
	d := &v1ServerDeps{}
	d.SetInstallRoot("/", nil, true)
	abs, err := filepath.Abs(t.TempDir())
	if err != nil {
		t.Fatalf("Abs: %v", err)
	}
	if err := d.validateInstallDir(abs); err != nil {
		t.Errorf("t.TempDir() %q should validate: %v", abs, err)
	}
}
