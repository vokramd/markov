package secret

import (
	"bytes"
	"log"
	"reflect"
	"testing"
)

const token = "ghp_0123456789abcdef"

func TestMaskerString(t *testing.T) {
	var m Masker
	if got := m.String("token " + token); got != "token "+token {
		t.Errorf("an empty masker changed %q", got)
	}
	m.Add("GITHUB_TOKEN", token)
	m.Add("SHORT", "abc1234") // under MinLength: not masked
	m.Add("LONGER", token+"_extra")
	m.Add("PADDED", "  s3cr3t-value-padded\n")

	cases := map[string]string{
		"Authorization: token " + token:       "Authorization: token <secret:GITHUB_TOKEN>",
		"https://x/?t=" + token + "&a=1":      "https://x/?t=<secret:GITHUB_TOKEN>&a=1",
		"abc1234 stays":                       "abc1234 stays",
		"whole " + token + "_extra":           "whole <secret:LONGER>",
		"printed s3cr3t-value-padded, then":   "printed <secret:PADDED>, then",
		"spaced   s3cr3t-value-padded\n done": "spaced <secret:PADDED> done",
	}
	for in, want := range cases {
		if got := m.String(in); got != want {
			t.Errorf("String(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMaskerEscapedForm(t *testing.T) {
	var m Masker
	m.Add("PASS", "p@ss word/with+chars")
	got := m.String("q=p%40ss+word%2Fwith%2Bchars")
	if got != "q=<secret:PASS>" {
		t.Errorf("query-escaped form not masked: %q", got)
	}
}

func TestMaskerAddNestedAndPath(t *testing.T) {
	var m Masker
	m.Add("creds", map[string]any{"a": "first-secret-value", "list": []any{"second-secret-value", 42}})
	m.AddPath("mint.body.token", map[string]any{"body": map[string]any{"token": "third-secret-value", "login": "markov-bot-login"}}, "body.token")
	m.AddPath("mint.missing", map[string]any{"body": "x"}, "body.token")
	got := m.String("first-secret-value second-secret-value third-secret-value markov-bot-login")
	want := "<secret:creds> <secret:creds> <secret:mint.body.token> markov-bot-login"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestMaskerValueCopies(t *testing.T) {
	var m Masker
	m.Add("T", token)
	type result struct {
		Header string `json:"header"`
	}
	in := map[string]any{
		"url":     "https://x/?t=" + token,
		"n":       3,
		token:     "as a key",
		"list":    []any{token, true},
		"strings": []string{"a", token},
		"typed":   map[string]string{"h": token},
		"struct":  result{Header: "token " + token},
	}
	out := m.Value(in).(map[string]any)
	if in["url"] != "https://x/?t="+token || in["list"].([]any)[0] != token {
		t.Fatal("Value changed its input")
	}
	want := map[string]any{
		"url":        "https://x/?t=<secret:T>",
		"n":          3,
		"<secret:T>": "as a key",
		"list":       []any{"<secret:T>", true},
		"strings":    []any{"a", "<secret:T>"},
		"typed":      map[string]any{"h": "<secret:T>"},
		"struct":     map[string]any{"header": "token <secret:T>"},
	}
	if !reflect.DeepEqual(out, want) {
		t.Errorf("Value =\n%#v\nwant\n%#v", out, want)
	}
}

func TestWriterMasksLogLines(t *testing.T) {
	var m Masker
	var buf bytes.Buffer
	l := log.New(m.Writer(&buf), "", 0)
	l.Printf("before %s", token)
	m.Add("T", token)
	l.Printf("after %s", token)
	if got := buf.String(); got != "before "+token+"\nafter <secret:T>\n" {
		t.Errorf("log = %q", got)
	}
}

func TestLooksSecret(t *testing.T) {
	for _, name := range []string{"GITHUB_TOKEN", "github_admin_token", "ANTHROPIC_API_KEY", "apikey", "db_password", "PASSWD", "CLIENT_SECRET", "aws_credentials", "ssh_private_key"} {
		if !LooksSecret(name) {
			t.Errorf("%s should look secret", name)
		}
	}
	for _, name := range []string{"GITHUB_USER", "max_tokens", "token_count", "keys", "markov_event_logs", "secret_name_list"} {
		if LooksSecret(name) {
			t.Errorf("%s should not look secret", name)
		}
	}
}
