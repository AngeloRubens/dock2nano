import subprocess, sys

# One mutation of cmd/compose.go per job; the tests of compose have to fail (KILLED).
M = {
    # "plain" gives the suffixed name
    "plain-has-the-suffix": (
        "	case dnsNamesPlain:\n		return []string{host}, nil",
        "	case dnsNamesPlain:\n		return []string{host + \".service\"}, nil"),
    # "both" gives only the plain name
    "both-is-only-plain": (
        "return []string{host, host + \".service\"}, nil",
        "return []string{host}, nil"),
    # the empty value (the file does not say) is not the default any more
    "empty-is-not-the-default": (
        "case \"\", dnsNamesService:", "case dnsNamesService:"),
    # an unknown value is taken for the default
    "unknown-is-the-default": (
        "	return nil, fmt.Errorf(\"dns_names: %q is not known,",
        "	return []string{host + \".service\"}, nil\n	return nil, fmt.Errorf(\"dns_names: %q is not known,"),
    # only the first name is added
    "only-the-first-name": (
        "	for _, name := range all {", "	for _, name := range all[:1] {"),
    # an unknown value is not reported by registerService
    "register-ignores-unknown": (
        "	all, err := dnsNamesOf(names, host)\n	if err != nil {\n		return err\n	}\n",
        "	all, _ := dnsNamesOf(names, host)\n"),
    # the file key is not read
    "key-not-read": ('`yaml:"dns_names"`', '`yaml:"dns_names_off"`'),
}

name = sys.argv[1]
old, new = M[name]
path = "cmd/compose.go"
s = open(path).read()
if s.count(old) != 1:
    sys.exit("%s: pattern found %d times" % (name, s.count(old)))
open(path, "w").write(s.replace(old, new))
r = subprocess.run(["go", "test", "./cmd/...", "-count", "1", "-timeout", "120s", "-run", "DNSNames|RegisterService|AddDNS"],
                   capture_output=True, text=True)
out = r.stdout + r.stderr
print(out[-2500:])
if r.returncode == 0:
    sys.exit("SURVIVED: %s" % name)
if "[build failed]" in out or "setup failed" in out:
    sys.exit("BROKEN MUTATION: %s" % name)
print("KILLED: %s" % name)
