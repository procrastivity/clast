package pi_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/procrastivity/clast/internal/source/pi"
)

func newHome(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func idsAt(t *testing.T, s *pi.Source) ([]string, []string) {
	t.Helper()
	found, diags, err := discover(t, s)
	if err != nil {
		t.Fatal(err)
	}
	return ids(found), diagStrings(diags)
}

func sid(n string) string { return idPrefix + "00000000" + n }

func TestRootResolution(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		home := newHome(t)
		writeSession(t, filepath.Join(home, ".pi", "agent", "sessions", "--x--"), sid("0001"))
		got, diags := idsAt(t, pi.NewWithEnv(envOf(home, nil)))
		if !reflect.DeepEqual(got, []string{sid("0001")}) || len(diags) != 0 {
			t.Errorf("got %v %q", got, diags)
		}
	})

	t.Run("env session dir beats settings, default stays as history", func(t *testing.T) {
		home := newHome(t)
		envDir, setDir := filepath.Join(home, "envsess"), filepath.Join(home, "setsess")
		writeSession(t, envDir, sid("0001"))
		writeSession(t, setDir, sid("0002"))
		writeSession(t, filepath.Join(home, ".pi", "agent", "sessions", "--x--"), sid("0003"))
		writeFile(t, filepath.Join(home, ".pi", "agent", "settings.json"), `{"sessionDir":"`+setDir+`"}`)
		s := pi.NewWithEnv(envOf(home, map[string]string{"PI_CODING_AGENT_SESSION_DIR": envDir}))
		got, diags := idsAt(t, s)
		if !reflect.DeepEqual(got, []string{sid("0001"), sid("0003")}) || len(diags) != 0 {
			t.Errorf("got %v %q", got, diags)
		}
	})

	t.Run("settings sessionDir", func(t *testing.T) {
		home := newHome(t)
		setDir := filepath.Join(home, "setsess")
		writeSession(t, setDir, sid("0002"))
		writeSession(t, filepath.Join(home, ".pi", "agent", "sessions", "--x--"), sid("0003"))
		writeFile(t, filepath.Join(home, ".pi", "agent", "settings.json"), `{"sessionDir":"`+setDir+`","other":1}`)
		got, diags := idsAt(t, pi.NewWithEnv(envOf(home, nil)))
		if !reflect.DeepEqual(got, []string{sid("0002"), sid("0003")}) || len(diags) != 0 {
			t.Errorf("got %v %q", got, diags)
		}
	})

	t.Run("PI_CODING_AGENT_DIR moves root and settings, default stays", func(t *testing.T) {
		home := newHome(t)
		agent := filepath.Join(home, "elsewhere")
		setDir := filepath.Join(home, "setsess")
		writeSession(t, filepath.Join(agent, "sessions", "--x--"), sid("0001"))
		writeSession(t, setDir, sid("0002"))
		writeSession(t, filepath.Join(home, ".pi", "agent", "sessions", "--x--"), sid("0003"))
		// A settings file in the default agent dir must be ignored now.
		writeFile(t, filepath.Join(home, ".pi", "agent", "settings.json"), `{"sessionDir":"/nonexistent/ignored"}`)
		s := pi.NewWithEnv(envOf(home, map[string]string{"PI_CODING_AGENT_DIR": agent}))
		got, _ := idsAt(t, s)
		if !reflect.DeepEqual(got, []string{sid("0001"), sid("0003")}) {
			t.Errorf("got %v", got)
		}
		// And the moved dir's own settings.json applies.
		writeFile(t, filepath.Join(agent, "settings.json"), `{"sessionDir":"`+setDir+`"}`)
		got, _ = idsAt(t, s)
		if !reflect.DeepEqual(got, []string{sid("0001"), sid("0002"), sid("0003")}) {
			t.Errorf("with settings: got %v", got)
		}
	})

	t.Run("tilde expansion", func(t *testing.T) {
		home := newHome(t)
		writeSession(t, filepath.Join(home, "tilde"), sid("0001"))
		s := pi.NewWithEnv(envOf(home, map[string]string{"PI_CODING_AGENT_SESSION_DIR": "~/tilde"}))
		got, diags := idsAt(t, s)
		if !reflect.DeepEqual(got, []string{sid("0001")}) || len(diags) != 0 {
			t.Errorf("got %v %q", got, diags)
		}
	})

	t.Run("overlapping candidates are de-duplicated", func(t *testing.T) {
		home := newHome(t)
		def := filepath.Join(home, ".pi", "agent", "sessions")
		writeSession(t, filepath.Join(def, "--x--"), sid("0001"))
		link := filepath.Join(home, "link")
		must(t, os.Symlink(def, link))
		for _, v := range []string{def, def + "/", link} {
			s := pi.NewWithEnv(envOf(home, map[string]string{
				"PI_CODING_AGENT_SESSION_DIR": v,
				"PI_CODING_AGENT_DIR":         filepath.Join(home, ".pi", "agent"),
			}))
			got, diags := idsAt(t, s)
			if !reflect.DeepEqual(got, []string{sid("0001")}) || len(diags) != 0 {
				t.Errorf("%s: got %v %q", v, got, diags)
			}
		}
	})
}

func TestRelativeOverridesAreDiagnosed(t *testing.T) {
	for _, tc := range []struct {
		name  string
		vars  map[string]string
		write func(home string)
		where string
	}{
		{"env session dir", map[string]string{"PI_CODING_AGENT_SESSION_DIR": "rel/sessions"}, nil, "PI_CODING_AGENT_SESSION_DIR"},
		{"env agent dir", map[string]string{"PI_CODING_AGENT_DIR": "rel/agent"}, nil, "PI_CODING_AGENT_DIR"},
		{"settings", nil, func(home string) {
			writeFile(t, filepath.Join(home, ".pi", "agent", "settings.json"), `{"sessionDir":"rel/x"}`)
		}, "settings.json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := newHome(t)
			writeSession(t, filepath.Join(home, ".pi", "agent", "sessions", "--x--"), sid("0001"))
			if tc.write != nil {
				tc.write(home)
			}
			found, diags, err := discover(t, pi.NewWithEnv(envOf(home, tc.vars)))
			if err != nil || len(found) != 1 {
				t.Fatalf("found=%v err=%v: the default root must still apply", found, err)
			}
			if len(diags) != 1 || !strings.Contains(diags[0].Path, tc.where) || !strings.Contains(diags[0].Err.Error(), "relative") {
				t.Errorf("diags = %q", diagStrings(diags))
			}
		})
	}
}

func TestSettingsProblemsAreDiagnosed(t *testing.T) {
	t.Run("malformed", func(t *testing.T) {
		home := newHome(t)
		writeSession(t, filepath.Join(home, ".pi", "agent", "sessions", "--x--"), sid("0001"))
		writeFile(t, filepath.Join(home, ".pi", "agent", "settings.json"), `{not json`)
		found, diags, err := discover(t, pi.NewWithEnv(envOf(home, nil)))
		if err != nil || len(found) != 1 || len(diags) != 1 || !strings.Contains(diags[0].Err.Error(), "malformed settings.json") {
			t.Errorf("found=%v diags=%q err=%v", found, diagStrings(diags), err)
		}
	})
	t.Run("unreadable", func(t *testing.T) {
		skipIfRoot(t)
		home := newHome(t)
		writeSession(t, filepath.Join(home, ".pi", "agent", "sessions", "--x--"), sid("0001"))
		p := filepath.Join(home, ".pi", "agent", "settings.json")
		writeFile(t, p, `{}`)
		must(t, os.Chmod(p, 0))
		found, diags, err := discover(t, pi.NewWithEnv(envOf(home, nil)))
		if err != nil || len(found) != 1 || len(diags) != 1 || !errors.Is(diags[0].Err, fs.ErrPermission) {
			t.Errorf("found=%v diags=%q err=%v", found, diagStrings(diags), err)
		}
	})
	t.Run("absent settings and non-string sessionDir are quiet or diagnosed, never fatal", func(t *testing.T) {
		home := newHome(t)
		writeFile(t, filepath.Join(home, ".pi", "agent", "settings.json"), `{"sessionDir":5}`)
		_, diags, err := discover(t, pi.NewWithEnv(envOf(home, nil)))
		if err != nil || len(diags) != 1 {
			t.Errorf("diags=%q err=%v", diagStrings(diags), err)
		}
	})
}

func TestEnvDrivenAbsenceAndPresence(t *testing.T) {
	t.Run("all roots absent", func(t *testing.T) {
		home := newHome(t)
		s := pi.NewWithEnv(envOf(home, map[string]string{"PI_CODING_AGENT_DIR": filepath.Join(home, "nope")}))
		found, diags, err := discover(t, s)
		if found != nil || diags != nil || err != nil {
			t.Fatalf("got %v %v %v", found, diags, err)
		}
		perr := s.Present(t.Context())
		var pe *fs.PathError
		if !errors.As(perr, &pe) || !errors.Is(perr, fs.ErrNotExist) {
			t.Fatalf("Present = %v", perr)
		}
		if want := filepath.Join(home, "nope", "sessions"); pe.Path != want {
			t.Errorf("Present names %s, want the effective root %s", pe.Path, want)
		}
	})
	t.Run("one historical root is enough for Present", func(t *testing.T) {
		home := newHome(t)
		must(t, os.MkdirAll(filepath.Join(home, ".pi", "agent", "sessions"), 0o755))
		s := pi.NewWithEnv(envOf(home, map[string]string{"PI_CODING_AGENT_SESSION_DIR": filepath.Join(home, "gone")}))
		if err := s.Present(t.Context()); err != nil {
			t.Errorf("Present = %v", err)
		}
	})
	t.Run("one unreadable root among good ones is an item diagnostic", func(t *testing.T) {
		skipIfRoot(t)
		home := newHome(t)
		bad := filepath.Join(home, "bad")
		writeSession(t, bad, sid("0009"))
		writeSession(t, filepath.Join(home, ".pi", "agent", "sessions", "--x--"), sid("0001"))
		must(t, os.Chmod(bad, 0))
		t.Cleanup(func() { _ = os.Chmod(bad, 0o755) })
		s := pi.NewWithEnv(envOf(home, map[string]string{"PI_CODING_AGENT_SESSION_DIR": bad}))
		found, diags, err := discover(t, s)
		if err != nil || len(found) != 1 || len(diags) != 1 || diags[0].Path != bad {
			t.Errorf("found=%v diags=%q err=%v", found, diagStrings(diags), err)
		}
		if err := s.Present(t.Context()); err != nil {
			t.Errorf("Present = %v, want nil (another root is readable)", err)
		}
	})
	t.Run("home unresolvable but absolute override works", func(t *testing.T) {
		d := t.TempDir()
		writeSession(t, d, sid("0001"))
		env := pi.Env{
			Getenv:      func(k string) string { return map[string]string{"PI_CODING_AGENT_SESSION_DIR": d}[k] },
			UserHomeDir: func() (string, error) { return "", errors.New("no home") },
		}
		got, diags := idsAt(t, pi.NewWithEnv(env))
		if !reflect.DeepEqual(got, []string{sid("0001")}) || len(diags) != 1 {
			t.Errorf("got %v %q", got, diags)
		}
	})
	t.Run("nothing resolvable is an error", func(t *testing.T) {
		env := pi.Env{
			Getenv:      func(string) string { return "" },
			UserHomeDir: func() (string, error) { return "", errors.New("no home") },
		}
		s := pi.NewWithEnv(env)
		if _, _, err := discover(t, s); err == nil {
			t.Error("Discover: want error")
		}
		if err := s.Present(t.Context()); err == nil {
			t.Error("Present: want error")
		}
	})
}

func TestNewAtIgnoresEnvironment(t *testing.T) {
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", t.TempDir())
	root := t.TempDir()
	writeSession(t, filepath.Join(root, "--x--"), sid("0001"))
	got, diags := idsAt(t, pi.NewAt(root))
	if !reflect.DeepEqual(got, []string{sid("0001")}) || len(diags) != 0 {
		t.Errorf("got %v %q", got, diags)
	}
}

func TestIdentity(t *testing.T) {
	s := pi.NewAt("x")
	if s.Name() != "pi" || s.Model() != "file-tail" {
		t.Errorf("Name=%q Model=%q", s.Name(), s.Model())
	}
}
