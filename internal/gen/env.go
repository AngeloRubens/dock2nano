package gen

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/AngeloRubens/dock2nano/internal/compose"
	"github.com/AngeloRubens/dock2nano/internal/interp"
	"github.com/AngeloRubens/dock2nano/internal/plan"
)

// ServiceEnv returns the environment that Compose gives the service: the
// variables of the env_file files in order, then those of environment, which
// win. Values are interpolated with lookup (the environment of the machine)
// and a variable without a value in the file takes its value from lookup. Paths
// of env_file are relative to dir. The environment of the image is not part of
// it: the caller puts it below. The map is never nil.
//
// What cannot be carried over is returned as items for the service: a Note
// that names (never shows the value of) the variables that were not set and
// became empty or were left out, and an Unsupported item for an env_file that
// cannot be read or parsed, or a value that cannot be expanded.
func ServiceEnv(s *compose.Service, dir string, lookup func(string) (string, bool), readFile func(string) ([]byte, error)) (map[string]string, []plan.Item) {
	if lookup == nil {
		lookup = func(string) (string, bool) { return "", false }
	}
	if readFile == nil {
		readFile = os.ReadFile
	}
	env := map[string]string{}
	var items []plan.Item
	add := func(kind plan.Kind, key, msg string) {
		items = append(items, plan.Item{Service: s.Name, Kind: kind, Key: key, Message: msg})
	}
	for i, path := range s.EnvFile {
		key := fmt.Sprintf("env_file[%d]", i)
		full := path
		if !filepath.IsAbs(full) {
			full = filepath.Join(dir, full)
		}
		data, err := readFile(full)
		if err != nil {
			add(plan.Unsupported, key, fmt.Sprintf("%s cannot be read (%v); its variables are not set", path, err))
			continue
		}
		kvs, unset, err := interp.ParseEnvFile(data, lookup)
		if err != nil {
			add(plan.Unsupported, key, fmt.Sprintf("%s cannot be parsed (%v); its variables are not set", path, err))
			continue
		}
		var absent []string
		for _, kv := range kvs {
			if kv.Set {
				env[kv.Name] = kv.Value
			} else {
				absent = append(absent, kv.Name)
			}
		}
		if len(unset) > 0 {
			add(plan.Note, key, "variables used in the file are not set on this machine and became empty: "+strings.Join(unset, ", "))
		}
		if len(absent) > 0 {
			add(plan.Note, key, "variables named without a value are not set on this machine and are not set in the package: "+strings.Join(absent, ", "))
		}
	}
	for _, e := range s.Environment {
		key := "environment." + e.Name
		if !e.Set {
			if v, ok := lookup(e.Name); ok {
				env[e.Name] = v
			} else {
				add(plan.Note, key, "the variable has no value in the file and is not set on this machine; it is not set in the package")
			}
			continue
		}
		v, unset, err := interp.Expand(e.Value, lookup)
		if err != nil {
			add(plan.Unsupported, key, fmt.Sprintf("the value cannot be expanded (%v); the variable is not set", err))
			continue
		}
		if len(unset) > 0 {
			add(plan.Note, key, "variables used in the value are not set on this machine and became empty: "+strings.Join(unset, ", "))
		}
		env[e.Name] = v
	}
	return env, items
}
