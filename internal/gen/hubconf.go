package gen

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/AngeloRubens/dock2nano/internal/hub"
	"github.com/AngeloRubens/dock2nano/internal/observe"
	"github.com/AngeloRubens/dock2nano/internal/plan"
	"github.com/AngeloRubens/dock2nano/internal/trace"
)

// maxIncludedFiles is the most files an include is expanded to.
const maxIncludedFiles = 200

// maxConfigFile is the largest configuration file that is read.
const maxConfigFile = 4 << 20

// writeTemp writes the new content of a configuration file into its
// temporary file; tests replace it. It is a variable of the package, so tests
// that replace it must not run in parallel.
var writeTemp = func(f *os.File, data []byte) error {
	_, err := f.Write(data)
	return err
}

// sourced is a top-level directive and where it was found.
type sourced struct {
	hub.Directive

	where string
}

// cfgState says what became of the configuration of the program.
type cfgState int

const (
	// cfgNone: nothing could be done.
	cfgNone cfgState = iota
	// cfgChanged: the file was changed.
	cfgChanged
	// cfgSatisfied: every directive for one process is already set.
	cfgSatisfied
	// cfgPartial: the file was changed, and some directives could not be set.
	cfgPartial
)

// hubConfig sets, in the configuration file of the user, the directives that
// make the program run as one process, with the values of hints/hub-tables.yaml
// (never the values of the hub package). A directive that the command line
// (-g) or an included file already sets is not written again, because the
// program refuses a directive given twice. It reports what was done and where
// the configuration of the hub package and the one of the user differ. The
// file is reached only through the sysroot, so a link cannot lead out of it.
// The second result tells whether the file was changed.
func hubConfig(u HubUse, obs *observe.Result) ([]plan.Item, cfgState) {
	program := path.Base(obs.Program)
	sp, ok := hub.SingleProcessFor(program)
	if !ok {
		return []plan.Item{u.item(plan.Note, "hub config", "no directives that make "+program+" run as one process are known; its configuration was not changed")}, cfgNone
	}
	hubText := ""
	for _, p := range sp.ConfigPaths {
		if data, ok := u.Recipe.Files[p]; ok {
			hubText = string(data)
			break
		}
	}
	fail := func(why string) ([]plan.Item, cfgState) {
		return []plan.Item{u.item(plan.Unsupported, "hub config", why+"; the directives "+settingsText(sp.Settings)+" were not applied")}, cfgNone
	}
	root, err := os.OpenRoot(obs.Sysroot)
	if err != nil {
		return fail("the files carried from your image cannot be opened (" + err.Error() + ")")
	}
	defer root.Close()
	logical, inImage, err := userConfig(root, obs, sp)
	if err != nil {
		return fail(err.Error())
	}
	data, mode, err := readInRoot(root, inImage)
	if err != nil {
		return fail("the configuration file " + inImage + " cannot be read (" + err.Error() + ")")
	}
	text := string(data)
	where := inImage
	if logical != inImage {
		where = logical + " (the file " + inImage + " in the image)"
	}

	external, noAdd := externalDirectives(root, obs, sp, logical, text)
	var items []plan.Item
	var apply []hub.Setting
	setElsewhere := map[string]bool{}
	for _, st := range sp.Settings {
		ext := findSourced(external, st.Name)
		switch {
		case ext == nil:
			apply = append(apply, st)
		case sameValue(ext.Args, st.Value):
			setElsewhere[st.Name] = true
			items = append(items, u.item(plan.Note, "hub config", st.Name+" "+st.Value+" is already set by "+ext.where+"; it is not written again, because the program refuses a directive given twice"))
		default:
			return append(items, u.item(plan.Unsupported, "hub config", st.Name+" is set to "+ext.Args+" by "+ext.where+", and the program needs "+st.Value+" to run as one process; it cannot be set again in the file, so nothing was changed in the configuration")), cfgNone
		}
	}
	out, changes, skipped := hub.SetTopLevel(text, apply, noAdd == "")
	if len(changes) > 0 {
		if err := replaceInRoot(root, strings.TrimPrefix(inImage, "/"), []byte(out), mode); err != nil {
			return append(items, u.item(plan.Unsupported, "hub config", "the configuration file "+inImage+" cannot be replaced ("+err.Error()+"); the directives "+settingsText(apply)+" were not applied")), cfgNone
		}
		items = append(items, u.item(plan.Approximated, "hub config", "in the copy in the package of "+where+", at the top level: "+changesText(changes)+" (the values for one process from the table of this tool, not from the hub package; if the file is a bind mount, the file on your machine was not changed). Everything else in the file, the ports, servers and paths included, is as it was"))
	} else if len(skipped) == 0 {
		for _, st := range apply {
			items = append(items, u.item(plan.Note, "hub config", "the copy in the package of "+where+" already has "+st.Name+" "+st.Value+" at the top level; nothing was changed"))
		}
	}
	if len(skipped) > 0 {
		items = append(items, u.item(plan.Unsupported, "hub config", "the file "+where+" "+noAdd+", which may hold the directives; "+settingsText(skipped)+" were not added, because a second copy would stop the program; if it still starts several processes, set them there"))
	}
	state := cfgNone
	switch {
	case len(changes) > 0 && len(skipped) > 0:
		state = cfgPartial
	case len(changes) > 0:
		state = cfgChanged
	case len(skipped) == 0:
		state = cfgSatisfied
	}
	return append(items, hubDivergence(u, sp, hubText, out, setElsewhere)...), state
}

// flagValues are the values given to an option of the command line, in both
// forms: "-g X" and "-gX".
func flagValues(argv []string, flag string) []string {
	if flag == "" {
		return nil
	}
	var out []string
	for i, a := range argv {
		switch {
		case a == flag && i+1 < len(argv):
			out = append(out, argv[i+1])
		case strings.HasPrefix(a, flag) && len(a) > len(flag):
			out = append(out, a[len(flag):])
		}
	}
	return out
}

// sameValue compares two values of a directive: quotes around them do not
// count, and neither does the case (on and ON are the same to nginx).
func sameValue(a, b string) bool {
	return strings.EqualFold(unquote(a), unquote(b))
}

// unquote takes off one pair of matching quotes.
func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

func findSourced(list []sourced, name string) *sourced {
	for i := range list {
		if list[i].Name == name {
			return &list[i]
		}
	}
	return nil
}

// externalDirectives are the top-level directives that are set outside the
// text of the file: on the command line, and in the files it includes. When
// they cannot all be known, the second result says why, and nothing may be
// added to the file.
func externalDirectives(root *os.Root, obs *observe.Result, sp hub.SingleProcess, conf, text string) ([]sourced, string) {
	var out []sourced
	// the program keeps only the last value of the option
	if given := flagValues(obs.Argv, sp.ParamsFlag); len(given) > 0 {
		for _, d := range hub.ParseDirectives(given[len(given)-1]) {
			switch {
			case d.Block != "" || d.IsBlock:
			case d.Name == "include":
				return out, "has an include on the command line (" + sp.ParamsFlag + "), which this tool does not expand"
			default:
				out = append(out, sourced{d, "the command line (" + sp.ParamsFlag + ")"})
			}
		}
	}
	inc, why := includedDirectives(root, obs.Sysroot, conf, text)
	return append(out, inc...), why
}

// includedDirectives expands the top-level includes of a configuration inside
// the sysroot and returns the top-level directives of the files. The second
// result says why it could not be done completely: a pattern with a variable
// or with a wildcard in a directory, an include inside an included file, too
// many files, a file that cannot be read, or a block of Lua, whose body the
// reader of the file does not follow.
func includedDirectives(root *os.Root, sysroot, conf, text string) ([]sourced, string) {
	if hasLua(text) {
		return nil, "has a Lua block, which this tool does not read"
	}
	var out []sourced
	files := 0
	for _, d := range hub.ParseDirectives(text) {
		if d.Block != "" || d.IsBlock || d.Name != "include" {
			continue
		}
		pattern := strings.Trim(d.Args, `"'`)
		if strings.Contains(pattern, "$") || strings.ContainsAny(path.Dir(pattern), "*?[") {
			return nil, "includes " + pattern + ", which this tool cannot expand"
		}
		if !path.IsAbs(pattern) {
			pattern = path.Join(path.Dir(conf), pattern)
		}
		dir, base := path.Split(pattern)
		names := []string{base}
		if strings.ContainsAny(base, "*?[") {
			names = nil
			entries, err := fs.ReadDir(root.FS(), strings.TrimPrefix(path.Clean(trace.InSysroot(sysroot, dir)), "/"))
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return nil, "includes " + pattern + ", whose directory cannot be read"
			}
			for _, e := range entries {
				// a wildcard does not match a name that starts with a dot
				if strings.HasPrefix(e.Name(), ".") && !strings.HasPrefix(base, ".") {
					continue
				}
				if ok, _ := path.Match(base, e.Name()); ok {
					names = append(names, e.Name())
				}
			}
		}
		for _, n := range names {
			if files++; files > maxIncludedFiles {
				return nil, "includes more than " + fmt.Sprint(maxIncludedFiles) + " files"
			}
			file := trace.InSysroot(sysroot, path.Join(dir, n))
			fi, err := root.Lstat(strings.TrimPrefix(file, "/"))
			if err != nil || !fi.Mode().IsRegular() {
				continue
			}
			data, _, err := readInRoot(root, file)
			if err != nil {
				return nil, "includes " + file + ", which cannot be read"
			}
			if hasLua(string(data)) {
				return nil, "includes " + file + ", which has a Lua block"
			}
			for _, inner := range hub.ParseDirectives(string(data)) {
				switch {
				case inner.Block != "" || inner.IsBlock:
				case inner.Name == "include":
					return nil, "includes " + file + ", which includes other files"
				default:
					out = append(out, sourced{inner, "the included file " + file})
				}
			}
		}
	}
	return out, ""
}

// hasLua tells whether a configuration has a block of Lua, at any depth.
func hasLua(text string) bool {
	for _, d := range hub.ParseDirectives(text) {
		if d.IsBlock && strings.HasSuffix(d.Name, "_by_lua_block") {
			return true
		}
	}
	return false
}

// userConfig finds the configuration file of the user among the files carried
// from the image: the one named by the option of the program (last one wins),
// else the first of the usual places that exists, those that the observation
// saw the program read first. The result is the path as it is named, and the
// path of the file it leads to inside the image, with the links followed
// within the sysroot (never outside it). The file is checked once more
// through the root of the sysroot.
func userConfig(root *os.Root, obs *observe.Result, sp hub.SingleProcess) (logical, inImage string, err error) {
	places := sp.ConfigPaths
	if given := flagValues(obs.Argv, sp.ConfigFlag); len(given) > 0 {
		last := given[len(given)-1]
		if !strings.HasPrefix(last, "/") {
			return "", "", fmt.Errorf("the program is started with %s %s, a relative path, which is not followed", sp.ConfigFlag, last)
		}
		places = []string{last}
	} else {
		var seen, rest []string
		for _, p := range places {
			if contains(obs.ConfigurationFiles, p) {
				seen = append(seen, p)
			} else {
				rest = append(rest, p)
			}
		}
		places = append(seen, rest...)
	}
	for _, p := range places {
		p = path.Clean(p)
		resolved := trace.InSysroot(obs.Sysroot, p)
		fi, err := root.Lstat(strings.TrimPrefix(resolved, "/"))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", "", fmt.Errorf("the configuration file %s cannot be reached inside the files carried from your image (%v), so it is not edited", p, err)
		}
		if !fi.Mode().IsRegular() {
			return "", "", fmt.Errorf("the configuration file %s is not a regular file, so it is not edited", p)
		}
		return p, resolved, nil
	}
	return "", "", fmt.Errorf("the configuration file was not found among the files carried from your image (looked at %s)", strings.Join(places, ", "))
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// readInRoot reads a file of the image through the root of the sysroot.
func readInRoot(root *os.Root, p string) ([]byte, fs.FileMode, error) {
	f, err := root.Open(strings.TrimPrefix(p, "/"))
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	data, err := io.ReadAll(io.LimitReader(f, maxConfigFile+1))
	if err == nil && len(data) > maxConfigFile {
		err = errors.New("the file is too large")
	}
	return data, fi.Mode().Perm(), err
}

// replaceInRoot replaces a file of the image with new content, all at once:
// the content goes into a temporary file next to it, which takes the mode of
// the original and is then renamed over it. When anything fails the original
// is as it was and no temporary file is left.
func replaceInRoot(root *os.Root, name string, data []byte, mode fs.FileMode) error {
	dir, base := path.Split(name)
	var f *os.File
	var tmp string
	var err error
	for i := 0; i < 10; i++ {
		var salt [6]byte
		if _, err = rand.Read(salt[:]); err != nil {
			return err
		}
		tmp = dir + "." + base + ".dock2nano-" + hex.EncodeToString(salt[:])
		if f, err = root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode); !errors.Is(err, fs.ErrExist) {
			break
		}
	}
	if err != nil {
		return err
	}
	err = writeTemp(f, data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = root.Chmod(tmp, mode)
	}
	if err == nil {
		err = root.Rename(tmp, name)
	}
	if err != nil {
		root.Remove(tmp)
	}
	return err
}

// pathMissing tells whether a value names an absolute path (or a list of them,
// separated by colons) of which some is not in the files carried from the
// image. A value with no absolute path is not missing.
func pathMissing(sysroot, v string) bool {
	for _, part := range strings.Split(v, ":") {
		if !strings.HasPrefix(part, "/") {
			continue
		}
		if _, err := os.Lstat(filepath.Join(sysroot, filepath.FromSlash(trace.InSysroot(sysroot, path.Clean(part))))); err != nil {
			return true
		}
	}
	return false
}

// volumeAtLeast is the larger of two sizes written as "266m" or "2g"; the
// first when one cannot be read.
func volumeAtLeast(have, least string) string {
	h, ok1 := megabytes(have)
	l, ok2 := megabytes(least)
	if ok1 && ok2 && l > h {
		return least
	}
	return have
}

func megabytes(s string) (int, bool) {
	if len(s) < 2 {
		return 0, false
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil {
		return 0, false
	}
	switch s[len(s)-1] {
	case 'm':
		return n, true
	case 'g':
		return n * 1024, true
	}
	return 0, false
}
