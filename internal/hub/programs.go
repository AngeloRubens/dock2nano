package hub

import "strings"

// SingleProcess says how a program that starts several processes is made to
// run as one, by its configuration file.
type SingleProcess struct {
	// ConfigPaths are the places the configuration file has in an image or in
	// a package of the hub; the first one that exists is used.
	ConfigPaths []string

	// ConfigFlag is the option of the program that names the file.
	ConfigFlag string

	// Settings are the top-level directives that make it run in one process,
	// with the value to use when the package of the hub does not give one.
	Settings []Setting

	// Compare are the directives whose values are compared between the
	// configuration of the hub and the one of the user, for the report.
	Compare []string
}

// SingleProcessFor returns what is known about a program, by the name of its
// executable.
func SingleProcessFor(program string) (SingleProcess, bool) {
	switch strings.ToLower(program) {
	case "nginx":
		return nginxSingleProcess(), true
	}
	return SingleProcess{}, false
}

func nginxSingleProcess() SingleProcess {
	var sp SingleProcess
	sp.ConfigPaths = []string{"/etc/nginx/nginx.conf", "/usr/local/nginx/conf/nginx.conf", "/usr/local/etc/nginx/nginx.conf"}
	sp.ConfigFlag = "-c"
	sp.Settings = []Setting{{"master_process", "off"}, {"daemon", "off"}, {"worker_processes", "1"}}
	sp.Compare = []string{"listen", "server_name", "root", "pid", "error_log", "access_log"}
	return sp
}

// SaneValue tells whether a value taken from a package of the hub can be
// written into the configuration of the user: one short word.
func SaneValue(v string) bool {
	if v == "" || len(v) > 32 {
		return false
	}
	for _, c := range v {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.') {
			return false
		}
	}
	return true
}
