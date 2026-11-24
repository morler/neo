package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/tabwriter"
	"time"

	"github.com/owainlewis/neo/internal/auth"
	"github.com/owainlewis/neo/internal/config"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/logx"
	"github.com/owainlewis/neo/internal/session"
	"github.com/owainlewis/neo/internal/skills"
)

// failWriter fails every write, so a call that only reports on buffered output
// exercises its error branch without needing a broken filesystem.
type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestFlushTabwriterReportsWriteFailure(t *testing.T) {
	w := tabwriter.NewWriter(failWriter{}, 0, 0, 2, ' ', 0)
	_, _ = io.WriteString(w, "a\tb\n")

	var errOut strings.Builder
	if code := flushTabwriter(w, &errOut); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "write: write failed") {
		t.Fatalf("stderr = %q, want a write warning", errOut.String())
	}
}

func TestFlushTabwriterReportsSuccess(t *testing.T) {
	var out strings.Builder
	w := tabwriter.NewWriter(&out, 0, 0, 2, ' ', 0)
	_, _ = io.WriteString(w, "a\tb\n")

	var errOut strings.Builder
	if code := flushTabwriter(w, &errOut); code != 0 {
		t.Fatalf("code = %d, want 0 (stderr %q)", code, errOut.String())
	}
	if !strings.Contains(out.String(), "a") {
		t.Fatalf("stdout = %q, want the buffered row", out.String())
	}
}

func TestRestoreSessionCWDIsQuietWithoutASavedDirectory(t *testing.T) {
	var errOut strings.Builder
	before, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	restoreSessionCWD("", &errOut)

	if errOut.String() != "" {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}
	after, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("cwd changed from %q to %q", before, after)
	}
}

func TestRestoreSessionCWDWarnsForAMissingDirectory(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone")
	var errOut strings.Builder

	restoreSessionCWD(missing, &errOut)

	if !strings.Contains(errOut.String(), "is unavailable") {
		t.Fatalf("stderr = %q, want an unavailable-cwd warning", errOut.String())
	}
}

func TestRestoreSessionCWDWarnsForAFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	var errOut strings.Builder

	restoreSessionCWD(path, &errOut)

	if !strings.Contains(errOut.String(), "is unavailable") {
		t.Fatalf("stderr = %q, want a warning for a non-directory", errOut.String())
	}
}

func TestRestoreSessionCWDChangesToAnExistingDirectory(t *testing.T) {
	before, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(before) })
	target := t.TempDir()
	var errOut strings.Builder

	restoreSessionCWD(target, &errOut)

	if errOut.String() != "" {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}
	got, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if resolved, err := filepath.EvalSymlinks(got); err == nil {
		got = resolved
	}
	if want, err := filepath.EvalSymlinks(target); err == nil && got != want {
		t.Fatalf("cwd = %q, want %q", got, want)
	}
}

func TestSessionMetadataIsZeroForANilSession(t *testing.T) {
	if got := sessionMetadata(nil); got != (session.Metadata{}) {
		t.Fatalf("metadata = %#v, want the zero value", got)
	}
}

func TestSessionMetadataReturnsTheSessionMetadata(t *testing.T) {
	sess := &session.Session{Metadata: session.Metadata{ID: "sess_1", Model: "m"}}
	if got := sessionMetadata(sess); got.ID != "sess_1" || got.Model != "m" {
		t.Fatalf("metadata = %#v, want the session's own metadata", got)
	}
}

func TestLoadSkillsReturnsDefaultsWhenDisabled(t *testing.T) {
	off := false
	cfg := &config.Config{Features: config.Features{Skills: &off}}
	var errOut strings.Builder

	got := loadSkills(cfg, t.TempDir(), &errOut)

	if len(got) != len(skills.Defaults()) {
		t.Fatalf("skills = %d, want just the built-ins", len(got))
	}
	if errOut.String() != "" {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}
}

func TestLoadSkillsReturnsDefaultsWithoutAWorkingDirectory(t *testing.T) {
	var errOut strings.Builder
	if got := loadSkills(&config.Config{}, "", &errOut); len(got) != len(skills.Defaults()) {
		t.Fatalf("skills = %d, want just the built-ins", len(got))
	}
}

func TestLoadSkillsFallsBackOnADiscoveryError(t *testing.T) {
	// A file where the skills directory should be makes os.ReadDir fail with
	// something that is not "does not exist", which is the only discovery
	// failure the caller treats as a warning.
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".neo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".neo", "skills"), []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	var errOut strings.Builder

	got := loadSkills(&config.Config{}, root, &errOut)

	if len(got) != len(skills.Defaults()) {
		t.Fatalf("skills = %d, want the built-in fallback", len(got))
	}
	if !strings.Contains(errOut.String(), "warning: skills:") {
		t.Fatalf("stderr = %q, want a skills warning", errOut.String())
	}
}

func TestLoadSkillsDiscoversAProjectSkill(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".neo", "skills", "deploy")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: deploy\ndescription: ship it\n---\nShip the thing.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	var errOut strings.Builder

	got := loadSkills(&config.Config{}, root, &errOut)

	if errOut.String() != "" {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}
	if _, ok := skills.Find(got, "deploy"); !ok {
		t.Fatalf("discovered skills = %#v, want deploy", got)
	}
}

// TestCodexCredentialsTokenRequiresLogin pins the wrapped error a missing
// credential produces: callers check ErrLoginRequired to skip retries.
func TestCodexCredentialsTokenRequiresLogin(t *testing.T) {
	store := auth.NewStore(filepath.Join(t.TempDir(), "auth.json"))
	src := codexCredentials{ts: auth.NewTokenSource(store, auth.ProviderOpenAICodex)}

	access, account, err := src.Token(context.Background())

	if !errors.Is(err, auth.ErrLoginRequired) {
		t.Fatalf("err = %v, want ErrLoginRequired", err)
	}
	if access != "" || account != "" {
		t.Fatalf("token = (%q, %q), want empty", access, account)
	}
}

func TestCodexCredentialsTokenReturnsStoredCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	store := auth.NewStore(path)
	creds := auth.Credentials{
		AccessToken:  "access",
		AccountID:    "acct",
		RefreshToken: "refresh",
		ExpiresAt:    time.Now().Add(time.Hour),
	}
	if err := store.Set(auth.ProviderOpenAICodex, creds); err != nil {
		t.Fatal(err)
	}
	src := codexCredentials{ts: auth.NewTokenSource(store, auth.ProviderOpenAICodex)}

	access, account, err := src.Token(context.Background())

	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if access != "access" || account != "acct" {
		t.Fatalf("token = (%q, %q), want (access, acct)", access, account)
	}
}

func TestRunLogoutWithoutCredentialsSucceeds(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	streams, out, errOut := bufferedStdio()

	if code := runLogout(streams); code != 0 {
		t.Fatalf("code = %d, want 0 (stderr %q)", code, errOut.String())
	}
	if !strings.Contains(out.String(), "Logged out") {
		t.Fatalf("stdout = %q, want a logout confirmation", out.String())
	}
}

func TestRunLogoutFailsWithoutAHomeDirectory(t *testing.T) {
	t.Setenv("HOME", "")
	streams, _, errOut := bufferedStdio()

	if code := runLogout(streams); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "logout:") {
		t.Fatalf("stderr = %q, want a logout error", errOut.String())
	}
}

func TestUnavailableProviderReportsItsNameAndError(t *testing.T) {
	p := unavailableProvider{name: "worker", err: errors.New("boom")}
	if p.Name() != "worker" {
		t.Fatalf("name = %q, want worker", p.Name())
	}
	if _, err := p.Complete(context.Background(), llm.Request{}); err == nil {
		t.Fatal("Complete returned no error")
	}
}

func TestDoctorModelCheckRejectsAnEmptyModel(t *testing.T) {
	got := doctorModelCheck(&config.Config{})
	if got.Status != doctorFail || !strings.Contains(got.Detail, "model is empty") {
		t.Fatalf("check = %#v, want a failing empty-model detail", got)
	}
}

func TestDoctorModelCheckPassesWithAModel(t *testing.T) {
	got := doctorModelCheck(&config.Config{Model: "gpt-5.2"})
	if got.Status != doctorPass || got.Detail != "gpt-5.2" {
		t.Fatalf("check = %#v, want a passing check naming the model", got)
	}
}

func TestRunAgentsReportsWhenNoneAreDefined(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// The repository itself defines no agents; run from a temp dir so a
	// developer's .neo/agents cannot change the outcome.
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	streams, out, errOut := bufferedStdio()

	if code := runAgents(streams); code != 0 {
		t.Fatalf("code = %d, want 0 (stderr %q)", code, errOut.String())
	}
	if !strings.Contains(out.String(), "No agents defined") {
		t.Fatalf("stdout = %q, want the no-agents notice", out.String())
	}
}

func TestRunAgentsListsDiscoveredProfiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".neo", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "reviewer.md")
	if err := os.WriteFile(path, []byte("You review code.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	streams, out, errOut := bufferedStdio()

	if code := runAgents(streams); code != 0 {
		t.Fatalf("code = %d, want 0 (stderr %q)", code, errOut.String())
	}
	if !strings.Contains(out.String(), "reviewer") {
		t.Fatalf("stdout = %q, want the reviewer profile", out.String())
	}
}

func TestInitLoggingCreatesTheLogFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	logPath := filepath.Join(t.TempDir(), "logs", "neo.log")
	t.Setenv("NEO_LOG", logPath)
	t.Cleanup(func() { _ = logx.Close() })

	if err := initLogging(); err != nil {
		t.Fatalf("initLogging: %v", err)
	}
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("log file: %v", err)
	}
}

func TestInitLoggingRejectsAnUnwritablePath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// A file where the log's parent directory should be makes MkdirAll fail.
	parent := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(parent, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NEO_LOG", filepath.Join(parent, "neo.log"))
	t.Cleanup(func() { _ = logx.Close() })

	if err := initLogging(); err == nil {
		t.Fatal("initLogging accepted an unwritable log path")
	}
}

func TestListSessionsReportsAnEmptyStore(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	streams, out, errOut := bufferedStdio()

	if code := listSessions(context.Background(), streams); code != 0 {
		t.Fatalf("code = %d, want 0 (stderr %q)", code, errOut.String())
	}
	if !strings.Contains(out.String(), "no saved sessions") {
		t.Fatalf("stdout = %q, want the empty-store notice", out.String())
	}
}

func TestListSessionsPrintsSavedSessions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := session.NewStore(filepath.Join(home, ".neo", "sessions"))
	if _, err := store.Create(context.Background(), session.Metadata{
		Title: "Fix the parser",
		CWD:   "/tmp/project",
		Model: "gpt-5.2",
	}); err != nil {
		t.Fatal(err)
	}
	streams, out, errOut := bufferedStdio()

	if code := listSessions(context.Background(), streams); code != 0 {
		t.Fatalf("code = %d, want 0 (stderr %q)", code, errOut.String())
	}
	for _, want := range []string{"Fix the parser", "gpt-5.2", "/tmp/project", "ID"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("stdout = %q, want %q", out.String(), want)
		}
	}
}

func TestListSessionsFailsOnACorruptIndex(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".neo", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The index file name is an implementation detail of the session package,
	// so discover it by creating a session and then corrupting what appears.
	store := session.NewStore(dir)
	if _, err := store.Create(context.Background(), session.Metadata{Model: "m"}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() == "index.json" {
			if err := os.WriteFile(filepath.Join(dir, e.Name()), []byte("{"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	streams, _, errOut := bufferedStdio()

	if code := listSessions(context.Background(), streams); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "list sessions:") {
		t.Fatalf("stderr = %q, want a list error", errOut.String())
	}
}

func TestSearchSessionsReportsNoMatches(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	streams, out, errOut := bufferedStdio()

	if code := searchSessions(context.Background(), "needle", streams); code != 0 {
		t.Fatalf("code = %d, want 0 (stderr %q)", code, errOut.String())
	}
	if !strings.Contains(out.String(), "no matching sessions") {
		t.Fatalf("stdout = %q, want the no-match notice", out.String())
	}
}

func TestSearchSessionsPrintsMatches(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := session.NewStore(filepath.Join(home, ".neo", "sessions"))
	sess, err := store.Create(context.Background(), session.Metadata{Title: "Needle hunt", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	sess.Messages = []llm.Message{{
		Role:    llm.RoleUser,
		Content: []llm.ContentBlock{{Type: "text", Text: "find the needle please"}},
	}}
	if err := store.Save(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	streams, out, errOut := bufferedStdio()

	if code := searchSessions(context.Background(), "needle", streams); code != 0 {
		t.Fatalf("code = %d, want 0 (stderr %q)", code, errOut.String())
	}
	if !strings.Contains(out.String(), "Needle hunt") {
		t.Fatalf("stdout = %q, want the matching session", out.String())
	}
}

func TestSearchSessionsFailsOnAnEmptyQuery(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	streams, _, errOut := bufferedStdio()

	if code := searchSessions(context.Background(), "   ", streams); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "search sessions:") {
		t.Fatalf("stderr = %q, want a search error", errOut.String())
	}
}

func TestSearchSessionsWarnsAboutUnreadableSessions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".neo", "sessions")
	store := session.NewStore(dir)
	sess, err := store.Create(context.Background(), session.Metadata{Title: "t", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, sess.Metadata.ID+".json")); err != nil {
		t.Fatal(err)
	}
	streams, _, errOut := bufferedStdio()

	if code := searchSessions(context.Background(), "anything", streams); code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if !strings.Contains(errOut.String(), "warning: skipped session") {
		t.Fatalf("stderr = %q, want a skipped-session warning", errOut.String())
	}
}

func TestLoadProfileWithoutANameKeepsTheBuiltInPrompt(t *testing.T) {
	var errOut strings.Builder

	p, ok := loadProfile(t.TempDir(), "", &errOut)

	if !ok || p.Name != "" {
		t.Fatalf("profile = %#v (ok=%v), want the zero profile and true", p, ok)
	}
	if errOut.String() != "" {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}
}

func TestLoadProfileFailsLoudlyForAnUnknownAgent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var errOut strings.Builder

	_, ok := loadProfile(t.TempDir(), "nope", &errOut)

	if ok {
		t.Fatal("an unknown agent was accepted")
	}
	if !strings.Contains(errOut.String(), "not found") {
		t.Fatalf("stderr = %q, want a not-found error", errOut.String())
	}
}

func TestFinishHeadlessEncodesJSON(t *testing.T) {
	streams, out, errOut := bufferedStdio()

	finishHeadless(headlessOptions{json: true}, headlessResult{Final: "done"}, streams)

	if !strings.Contains(out.String(), `"final": "done"`) {
		t.Fatalf("stdout = %q, want an indented JSON result", out.String())
	}
	if errOut.String() != "" {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}
}

func TestFinishHeadlessPrintsFinalTextAndErrors(t *testing.T) {
	streams, out, errOut := bufferedStdio()

	finishHeadless(headlessOptions{}, headlessResult{Final: "answer", Error: "boom"}, streams)

	if !strings.Contains(out.String(), "answer") {
		t.Fatalf("stdout = %q, want the final answer", out.String())
	}
	if !strings.Contains(errOut.String(), "boom") {
		t.Fatalf("stderr = %q, want the error", errOut.String())
	}
}

func TestResumeSessionReportsAMissingSession(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	streams, _, errOut := bufferedStdio()

	if code := resumeSession(context.Background(), "sess_missing", "", streams); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "session not found") {
		t.Fatalf("stderr = %q, want a not-found error", errOut.String())
	}
}

func TestResumeSessionRejectsATraversalID(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	streams, _, errOut := bufferedStdio()

	if code := resumeSession(context.Background(), "../etc/passwd", "", streams); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "load session:") {
		t.Fatalf("stderr = %q, want a load error", errOut.String())
	}
}

func TestProviderModelChoicesForACustomProvider(t *testing.T) {
	cfg := &config.Config{
		Provider: "local",
		Providers: map[string]config.CustomProvider{
			"local": {Endpoint: "http://127.0.0.1:1234/v1", Models: []config.CustomModel{
				{ID: "tiny", Name: "Tiny", Description: "small"},
			}},
		},
	}

	got := providerModelChoices(cfg, "local")

	if len(got) != 1 || got[0].ID != "tiny" || got[0].Name != "Tiny" {
		t.Fatalf("choices = %#v, want the declared custom model", got)
	}
}

func TestModelChoicesDefaultsToAnthropic(t *testing.T) {
	got := modelChoices(&config.Config{}, "")
	if len(got) == 0 {
		t.Fatal("no default model choices")
	}
}

func TestProviderCredentialPresentForACustomProviderWithoutAKeyEnv(t *testing.T) {
	cfg := &config.Config{
		Provider:  "local",
		Providers: map[string]config.CustomProvider{"local": {Endpoint: "http://127.0.0.1:1234/v1"}},
	}
	if !providerCredentialPresent(cfg, "local") {
		t.Fatal("a custom provider without api_key_env needs no credential")
	}
}

func TestProviderCredentialPresentFollowsTheEnvironment(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	cfg := &config.Config{}

	if providerCredentialPresent(cfg, "anthropic") {
		t.Fatal("anthropic reported a credential with the variable unset")
	}
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	if !providerCredentialPresent(cfg, "anthropic") {
		t.Fatal("anthropic reported no credential with the variable set")
	}
}

func TestSessionChoicesMirrorTheSessionsTable(t *testing.T) {
	updated := time.Date(2026, 11, 24, 9, 30, 0, 0, time.Local)
	got := sessionChoices([]session.Metadata{
		{ID: "sess_a", Title: "first", Model: "gpt-5", CWD: "/repo", UpdatedAt: updated},
		{ID: "sess_b"},
	})

	if len(got) != 2 {
		t.Fatalf("choices = %#v, want two rows", got)
	}
	if got[0].ID != "sess_a" || got[0].Title != "first" {
		t.Fatalf("first row = %#v, want the saved id and title", got[0])
	}
	for _, want := range []string{"gpt-5", "/repo", "2026-11-24 09:30"} {
		if !strings.Contains(got[0].Description, want) {
			t.Fatalf("description = %q, want %q", got[0].Description, want)
		}
	}
	// A metadata row with no model or update time must still render: the
	// description is informational, never a reason to hide the session.
	if got[1].ID != "sess_b" || got[1].Description != "" {
		t.Fatalf("bare row = %#v, want no leftover separator", got[1])
	}
}

func TestDescribeSessionLoadErrorKeepsTheResumeWording(t *testing.T) {
	missing := describeSessionLoadError("sess_a", session.ErrNotFound)
	if missing == nil || missing.Error() != "session not found: sess_a" {
		t.Fatalf("error = %v, want the resume wording", missing)
	}
	broken := describeSessionLoadError("sess_a", errors.New("parse session sess_a: bad json"))
	if broken == nil || !strings.Contains(broken.Error(), "load session sess_a") {
		t.Fatalf("error = %v, want the load context", broken)
	}
}
