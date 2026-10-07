package main

import (
	"net/http"
	"strings"
	"testing"
)

// synctoken_test.go and the update-host cases below cover the two places where
// tjek's security posture depends on something other than careful downstream
// code: the strength of a secret the user chose, and the origin of a binary it
// is about to run.

func TestGeneratedTokensAreStrongAndUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		token, err := newSyncToken()
		if err != nil {
			t.Fatalf("newSyncToken: %v", err)
		}
		if seen[token] {
			t.Fatalf("newSyncToken returned a duplicate after %d draws: %q", i, token)
		}
		seen[token] = true
		if why := weakSyncToken(token); why != "" {
			t.Errorf("a generated token was judged weak: %s", why)
		}
		// It has to survive a shell, a URL and a YAML file without quoting,
		// or it gets mangled and re-typed shorter by hand.
		if strings.ContainsAny(token, " \t\n\"'`$&|<>;\\/+=") {
			t.Errorf("generated token needs quoting somewhere: %q", token)
		}
	}
}

func TestWeakTokenJudgement(t *testing.T) {
	cases := []struct {
		name  string
		token string
		weak  bool
	}{
		{"empty is somebody else's error", "", false},
		{"a word", "hunter2", true},
		{"short random", "aB3$xQ", true},
		{"long but one class", "correcthorsebatterystaplecorrect", true},
		{"long and mixed", "correct-Horse-Battery-Staple-42", false},
	}
	for _, c := range cases {
		if got := weakSyncToken(c.token) != ""; got != c.weak {
			t.Errorf("%s: weak = %v, want %v (%q)", c.name, got, c.weak, weakSyncToken(c.token))
		}
	}
}

// The warning names the problem without ever repeating the secret — the same
// rule `tjek doctor` output follows, since it is meant to be pasteable.
func TestWeakTokenWarningNeverQuotesTheToken(t *testing.T) {
	const token = "hunter2"
	if why := weakSyncToken(token); strings.Contains(why, token) {
		t.Errorf("the warning quotes the token: %q", why)
	}
}

// ── Where an update may come from ─────────────────────────────────────────────

// The asset URL arrives inside a JSON body, so it is data, not a constant.
// Following one that points off GitHub would hand the update path to whoever
// wrote that body.
func TestUpdateRefusesAssetsFromElsewhere(t *testing.T) {
	allowed := []string{
		"https://github.com/Morteningemann86/xtratjek/releases/download/v1/tjek",
		"https://objects.githubusercontent.com/foo",
		"https://release-assets.githubusercontent.com/bar", // the host GitHub moved to
	}
	for _, raw := range allowed {
		if err := checkAssetURL(raw); err != nil {
			t.Errorf("%s should be allowed: %v", raw, err)
		}
	}

	refused := []string{
		"http://github.com/Morteningemann86/xtratjek/releases/download/v1/tjek", // no TLS
		"https://github.com.evil.test/tjek",                                     // suffix that only looks right
		"https://evil.test/tjek",
		"https://githubXcom/tjek",
		"ftp://github.com/tjek",
		"://",
	}
	for _, raw := range refused {
		if err := checkAssetURL(raw); err == nil {
			t.Errorf("%s should be refused", raw)
		}
	}
}

// A pre-flight check on the URL is not enough on its own: the default client
// follows redirects, so a 302 off GitHub would walk straight past it.
func TestUpdateRefusesARedirectOffGitHub(t *testing.T) {
	srv := releaseServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://evil.test/tjek", http.StatusFound)
	})

	_, err := downloadReleaseAsset(releaseAsset{Name: "tjek", URL: srv.URL + "/tjek"}, t.TempDir()+"/tjek")
	if err == nil {
		t.Fatal("a redirect to a non-GitHub host was followed")
	}
	if !strings.Contains(err.Error(), "evil.test") {
		t.Errorf("error should name the host it refused, got %v", err)
	}
}

// ── Where a weak token is refused, and where it must not be ───────────────────

// The server token is the one tjek wholly owns, and ctrl+g is one keystroke
// away, so a weak value is refused outright rather than warned about.
func TestServerTokenEditorRefusesWeakTokens(t *testing.T) {
	m := settingsModel(t)
	m = openSetting(t, m, settingServerToken)
	if m.mode != modeEditServerToken {
		t.Fatalf("mode = %v, want modeEditServerToken", m.mode)
	}

	m = script(t, m, "hunter2", "enter")
	if m.syncCfg.ServerToken != "" {
		t.Errorf("a weak server token was stored: %q", m.syncCfg.ServerToken)
	}
	if m.mode != modeEditServerToken {
		t.Errorf("mode = %v after a refusal, want to stay in the editor", m.mode)
	}
	// The typed text has to survive, or the refusal costs the user their input.
	if got := m.textInput.Value(); got != "hunter2" {
		t.Errorf("input = %q after a refusal, want the typed text kept", got)
	}
	if m.err == "" {
		t.Error("a refusal said nothing about why")
	}
}

// ctrl+g fills the field with something the editor will accept, so the refusal
// above always has a way out that is one keystroke long.
func TestServerTokenEditorGeneratesAnAcceptableToken(t *testing.T) {
	m := settingsModel(t)
	m = openSetting(t, m, settingServerToken)
	m = sendKey(t, m, "ctrl+g")

	generated := m.textInput.Value()
	if weakSyncToken(generated) != "" {
		t.Fatalf("ctrl+g produced a token the editor would refuse: %q", generated)
	}
	m = sendKey(t, m, "enter")
	if m.syncCfg.ServerToken != generated {
		t.Errorf("ServerToken = %q, want the generated %q", m.syncCfg.ServerToken, generated)
	}
	if m.mode != modeNormal {
		t.Errorf("mode = %v, want the editor closed after a good token", m.mode)
	}
}

// Clearing must keep working: blank is a deliberate removal, not a weak token.
func TestServerTokenEditorStillClears(t *testing.T) {
	m := settingsModel(t)
	m.syncCfg.ServerToken = "kR7-server-side-secret-9fQ2xL"
	m = openSetting(t, m, settingServerToken)
	m.textInput.SetValue("")
	m = sendKey(t, m, "enter")
	if m.syncCfg.ServerToken != "" {
		t.Errorf("ServerToken = %q, want cleared", m.syncCfg.ServerToken)
	}
	if m.mode != modeNormal {
		t.Errorf("mode = %v, want the editor closed", m.mode)
	}
}

// The client token is the other end's choice, not tjek's. Refusing a weak one
// here would not make anything safer — it would make a server that already uses
// a short token unreachable, which is a worse outcome than the risk.
func TestClientTokenEditorAcceptsWhateverTheServerUses(t *testing.T) {
	m := settingsModel(t)
	m = openSetting(t, m, settingSyncToken)
	if m.mode != modeEditSyncToken {
		t.Fatalf("mode = %v, want modeEditSyncToken", m.mode)
	}
	m = script(t, m, "hunter2", "enter")
	if m.syncCfg.Token != "hunter2" {
		t.Errorf("Token = %q — the client must be able to match a weak server", m.syncCfg.Token)
	}
	if m.mode != modeNormal {
		t.Errorf("mode = %v, want the editor closed", m.mode)
	}
}
