// Package dockerfile reads a Dockerfile without changing it, so that the
// converter knows how the final image starts.
//
// The parser keeps what the converter needs as typed fields and records every
// instruction or option it does not model in Unknown, so that nothing is
// dropped in silence. Values are taken as written: ${VAR} references are not
// interpolated, and a value that holds a variable reference is listed in
// Unknown. Only a $ that is not escaped and not inside single quotes is a
// reference: ENV a=\$x and ENV a='$x' are literals. Quotes and escapes in the
// words of ENV, ARG, LABEL, EXPOSE, VOLUME, WORKDIR, USER, STOPSIGNAL, COPY,
// ADD and in the options of FROM, COPY, ADD, RUN and HEALTHCHECK are removed,
// as Docker does; EnvVar.Raw and Arg.RawDefault keep the value as written.
//
// A stage inherits nothing in the structs: the Env, WorkDir, User and the
// other fields of a stage are only what its own instructions say. When the
// Base of a stage is the name of an earlier stage, the caller resolves it with
// File.BaseStage and merges the settings itself.
package dockerfile

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// File is a parsed Dockerfile.
type File struct {
	// Args are the ARG instructions before the first FROM.
	Args []Arg
	// Stages are the build stages, in file order. Each FROM starts one.
	Stages []*Stage
	// Unknown lists, as "line N: what", every instruction, option or value
	// that is not modelled, from all the stages and from the part before the
	// first FROM. It repeats the Unknown lists of the stages, so a caller
	// that prints the file needs only this one.
	Unknown []string
}

// Final returns the last stage, which is the image that is built by default,
// or nil if the file has no stage.
func (f *File) Final() *Stage {
	if len(f.Stages) == 0 {
		return nil
	}
	return f.Stages[len(f.Stages)-1]
}

// BaseStage returns the earlier stage that s builds on, that is, the stage
// declared before s whose AS name is the Base of s (compared without regard to
// case). It returns nil if the Base of s is an image: a name that is the name
// of s itself ("FROM nginx AS nginx"), the name of a later stage, or a number
// is an image, as in Docker. It does not read stage numbers.
func (f *File) BaseStage(s *Stage) *Stage {
	for _, prev := range f.Stages {
		if prev == s {
			return nil
		}
		if prev.Name != "" && strings.EqualFold(prev.Name, s.Base) {
			return prev
		}
	}
	return nil
}

// Stage returns the stage with the given AS name (compared without regard to
// case) or, if there is none, the stage at the given index written in
// decimal, such as "0". It returns nil if neither exists. It is for the value
// of COPY --from and for build targets, where a number is a stage index; do
// not use it for the Base of a stage, use BaseStage.
func (f *File) Stage(nameOrIndex string) *Stage {
	for _, s := range f.Stages {
		if s.Name != "" && strings.EqualFold(s.Name, nameOrIndex) {
			return s
		}
	}
	i, err := strconv.Atoi(nameOrIndex)
	if err != nil || strconv.Itoa(i) != nameOrIndex || i < 0 || i >= len(f.Stages) {
		return nil
	}
	return f.Stages[i]
}

// Stage is one build stage, from a FROM to the next one.
type Stage struct {
	// Name is the AS name, empty if the stage has none.
	Name string
	// Base is the image or stage name after FROM, as written.
	Base string
	// Platform is the value of FROM --platform, empty if not given.
	Platform string
	// Line is the line of the FROM.
	Line int
	// Env are the ENV settings in file order. A name can appear more than
	// once; Environ gives the result.
	Env []EnvVar
	// Args are the ARG instructions of the stage.
	Args []Arg
	// WorkDir is the last WORKDIR, as written (it can be relative).
	WorkDir string
	// User is the last USER.
	User string
	// Entrypoint is the last ENTRYPOINT of this stage. In an image, an
	// ENTRYPOINT in a stage clears the CMD inherited from the base unless the
	// same stage sets a CMD (Cmd.Set); the caller that merges stages applies
	// that. The SHELL of the base stage applies to a shell-form Entrypoint
	// and Cmd of this stage.
	Entrypoint Command
	// Cmd is the last CMD of this stage; see Entrypoint for what it inherits.
	Cmd Command
	// CmdLine, EntrypointLine and HealthcheckLine are the lines of the last
	// CMD, ENTRYPOINT and HEALTHCHECK, 0 if the stage has none.
	CmdLine         int
	EntrypointLine  int
	HealthcheckLine int
	// Shell is the last SHELL, nil if there is none.
	Shell []string
	// Expose are the EXPOSE values as written: "80", "80/tcp", "8000-8010/udp".
	Expose []string
	// Volumes are the VOLUME paths.
	Volumes []string
	// Labels are the LABEL settings in file order.
	Labels []EnvVar
	// Healthcheck is the last HEALTHCHECK, nil if there is none.
	Healthcheck *Healthcheck
	// StopSignal is the last STOPSIGNAL.
	StopSignal string
	// Copies are the COPY and ADD instructions in file order.
	Copies []Copy
	// Runs are the RUN instructions in file order.
	Runs []Run
	// Unknown lists, as "line N: what", what the stage has that is not
	// modelled: ONBUILD, MAINTAINER, unknown instructions, options such as
	// RUN --mount, heredocs and values that hold a $.
	Unknown []string
}

// Environ returns the ENV settings of the stage with a later setting
// replacing an earlier one of the same name. The order is the order of the
// first definition. The settings of a parent stage are not included.
func (s *Stage) Environ() []EnvVar {
	var out []EnvVar
	index := map[string]int{}
	for _, e := range s.Env {
		if i, ok := index[e.Name]; ok {
			out[i].Value = e.Value
			out[i].Raw = e.Raw
			continue
		}
		index[e.Name] = len(out)
		out = append(out, e)
	}
	return out
}

// Command is a CMD, an ENTRYPOINT, a RUN or a healthcheck test. The JSON form
// gives Exec and the shell form gives Shell. Set is true when the instruction
// was given, even with an empty list: "CMD []" resets the command of the
// image, while no CMD keeps it.
type Command struct {
	Shell string
	Exec  []string
	Set   bool
}

// EnvVar is a name and a value, used for ENV and LABEL.
type EnvVar struct {
	Name  string
	Value string
	// Raw is the value as written, before quotes and escapes were removed.
	Raw string
}

// Arg is one ARG. HasDefault tells "ARG a=" (an empty default) from "ARG a".
type Arg struct {
	Name    string
	Default string
	// RawDefault is the default as written, before quotes and escapes were
	// removed.
	RawDefault string
	HasDefault bool
}

// Copy is a COPY or an ADD.
type Copy struct {
	// Instruction is "COPY" or "ADD".
	Instruction string
	// From is the value of --from, a stage name, an index or an image.
	From string
	// Sources are the sources as written, without the destination.
	Sources []string
	// Dest is the destination as written.
	Dest string
	// Chown is the value of --chown.
	Chown string
	// Chmod is the value of --chmod.
	Chmod string
	// Line is the line of the instruction.
	Line int
}

// Run is a RUN instruction. Its options (--mount and the others) and its
// heredoc form are listed in the Unknown of the stage.
type Run struct {
	Command Command
	Line    int
}

// Healthcheck is a HEALTHCHECK. None is true for HEALTHCHECK NONE.
type Healthcheck struct {
	None bool
	// Test is the command after CMD.
	Test Command
	// Options are the options as "--name=value", in file order, such as
	// "--interval=30s".
	Options []string
}

// ParseFile reads and parses the Dockerfile at path.
func ParseFile(path string) (*File, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	f, err := Parse(fh)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}
