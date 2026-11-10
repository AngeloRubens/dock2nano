import subprocess, sys

# One mutation of cmd/compose.go per job; the tests of compose have to fail (KILLED).
M = {
    # no search line
    "no-search-line": (
        "	if len(c.SearchDomains) > 0 {", "	if false && len(c.SearchDomains) > 0 {", "lepton/image.go"),
    # only the first domain is written
    "first-domain-only": (
        'strings.Join(c.SearchDomains, " ")', 'c.SearchDomains[0]', "lepton/image.go"),
    # the file key is not read
    "key-not-read": ('`yaml:"dns_search"`', '`yaml:"dns_search_off"`'),
    # the domains are not given to the unikernel
    "not-given-to-the-unikernel": ("	c.SearchDomains = search\n", ""),
    # a domain with a newline goes into the file
    "no-check": ('		if d == "" || strings.ContainsAny(d, " \\t\\r\\n#;/") {', '		if false {'),
}

name = sys.argv[1]
entry = M[name]
old, new = entry[0], entry[1]
path = entry[2] if len(entry) > 2 else "cmd/compose.go"
s = open(path).read()
if s.count(old) != 1:
    sys.exit("%s: pattern found %d times" % (name, s.count(old)))
open(path, "w").write(s.replace(old, new))
r = subprocess.run(["go", "test", "./cmd/...", "./lepton/...", "-count", "1", "-timeout", "120s", "-run", "DNSSearch|ResolvConf|RegisterService|AddDNS"],
                   capture_output=True, text=True)
out = r.stdout + r.stderr
print(out[-2500:])
if r.returncode == 0:
    sys.exit("SURVIVED: %s" % name)
if "[build failed]" in out or "setup failed" in out:
    sys.exit("BROKEN MUTATION: %s" % name)
print("KILLED: %s" % name)
