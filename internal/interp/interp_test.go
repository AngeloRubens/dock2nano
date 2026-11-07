package interp

import (
	"reflect"
	"strings"
	"testing"
)

func env(kv ...string) func(string) (string, bool) {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return func(n string) (string, bool) {
		v, ok := m[n]
		return v, ok
	}
}

func TestExpand(t *testing.T) {
	look := env("A", "a", "EMPTY", "", "B", "b b")
	tests := []struct {
		name, in, want string
		unset          []string
	}{
		{"plain text", "hello", "hello", nil},
		{"dollar name", "x-$A-y", "x-a-y", nil},
		{"braces", "${A}${A}", "aa", nil},
		{"name ends at a non name character", "$A.txt", "a.txt", nil},
		{"unset is empty and listed", "[$NOPE]", "[]", []string{"NOPE"}},
		{"unset listed once in order", "$Z $Y ${Z} ${Y}", "   ", []string{"Z", "Y"}},
		{"empty is set and not listed", "[$EMPTY]", "[]", nil},
		{"double dollar", "a$$b $$A", "a$b $A", nil},
		{"lone dollar is kept", "cost $ 5 $", "cost $ 5 $", nil},
		{"default when unset", "${NOPE:-d}", "d", nil},
		{"default when empty with colon", "${EMPTY:-d}", "d", nil},
		{"default when empty without colon keeps empty", "${EMPTY-d}", "", nil},
		{"default when unset without colon", "${NOPE-d}", "d", nil},
		{"default not used when set", "${A:-d}", "a", nil},
		{"default is nested", "${NOPE:-${A}-$B}", "a-b b", nil},
		{"default holds an unset variable", "${NOPE:-$Q}", "", []string{"Q"}},
		{"unused default is not expanded", "${A:-$Q}", "a", nil},
		{"replacement when set", "${A:+r}", "r", nil},
		{"replacement with colon when empty", "${EMPTY:+r}", "", nil},
		{"replacement without colon when empty", "${EMPTY+r}", "r", nil},
		{"replacement when unset", "${NOPE:+r}${NOPE+r}", "", nil},
		{"replacement is nested", "${A:+<${B}>}", "<b b>", nil},
		{"required and set", "${A:?m}${A?m}", "aa", nil},
		{"required empty without colon", "${EMPTY?m}", "", nil},
		{"escaped dollar inside a default", "${NOPE:-$$A}", "$A", nil},
		{"closing brace inside a nested default", "${NOPE:-${A}}x", "ax", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, unset, err := Expand(tt.in, look)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want || !reflect.DeepEqual(unset, tt.unset) {
				t.Errorf("Expand(%q) = %q, %v; want %q, %v", tt.in, got, unset, tt.want, tt.unset)
			}
		})
	}
}

func TestExpandNilLookup(t *testing.T) {
	got, unset, err := Expand("$A", nil)
	if err != nil || got != "" || !reflect.DeepEqual(unset, []string{"A"}) {
		t.Errorf("got %q, %v, %v", got, unset, err)
	}
}

func TestExpandErrors(t *testing.T) {
	look := env("EMPTY", "", "SECRET", "s3cret")
	tests := []struct {
		in      string
		wantErr string
	}{
		{"${NOPE:?must be set}", "NOPE"},
		{"${NOPE?must be set}", "must be set"},
		{"${EMPTY:?need it}", "EMPTY"},
		{"${NOPE:-${OTHER:?deep}}", "OTHER"},
		{"${A", "not closed"},
		{"${}", "invalid"},
		{"${1A}", "invalid"},
		{"${A%x}", "invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			_, _, err := Expand(tt.in, look)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want one holding %q", err, tt.wantErr)
			}
		})
	}
	_, _, err := Expand("${SECRET%x}", look)
	if err == nil || strings.Contains(err.Error(), "s3cret") {
		t.Errorf("error = %v: it must not hold the value", err)
	}
}

func TestParseEnvFile(t *testing.T) {
	look := env("HOME", "/root", "FROM_HOST", "hv")
	src := "\xef\xbb\xbf# comment\r\n\r\nA=1\r\nexport B=two words\n  C = x \nD=val # trailing\nE=val#notcomment\n" +
		"F='sq ${HOME} # kept'\nG=\"dq ${HOME}\\n\\\"q\\\" \\$HOME # kept\" # comment\nH=${HOME}/x\nFROM_HOST\nABSENT\nI=\nJ=\"multi\nline\"\nK=$$HOME\nL=${NOPE:-dflt}\nM=$MISSING\n"
	got, unset, err := ParseEnvFile([]byte(src), look)
	if err != nil {
		t.Fatal(err)
	}
	want := []KV{
		{"A", "1", true},
		{"B", "two words", true},
		{"C", "x", true},
		{"D", "val", true},
		{"E", "val#notcomment", true},
		{"F", "sq ${HOME} # kept", true},
		{"G", "dq /root\n\"q\" $HOME # kept", true},
		{"H", "/root/x", true},
		{"FROM_HOST", "hv", true},
		{"ABSENT", "", false},
		{"I", "", true},
		{"J", "multi\nline", true},
		{"K", "$HOME", true},
		{"L", "dflt", true},
		{"M", "", true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("entries =\n%+v\nwant\n%+v", got, want)
	}
	if !reflect.DeepEqual(unset, []string{"MISSING"}) {
		t.Errorf("unset = %v, want [MISSING]", unset)
	}
}

func TestParseEnvFileErrors(t *testing.T) {
	look := env("SECRET", "s3cret")
	tests := []struct{ name, in, want string }{
		{"not an assignment", "=x\n", "line 1"},
		{"space in name", "A B=1\n", "line 1"},
		{"open single quote", "A=1\nB='x\n", "line 2"},
		{"open double quote", "B=\"x\n", "double quote"},
		{"text after the quote", "B='x' y\n", "after the closing quote"},
		{"required", "A=${NOPE:?needed}\n", "NOPE"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := ParseEnvFile([]byte(tt.in), look)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want one holding %q", err, tt.want)
			}
		})
	}
	_, _, err := ParseEnvFile([]byte("A=$SECRET 'x\nB='y' $SECRET\n"), look)
	if err == nil || strings.Contains(err.Error(), "s3cret") {
		t.Errorf("error = %v: it must not hold a value", err)
	}
}
