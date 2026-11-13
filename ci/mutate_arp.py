import subprocess, sys

# One mutation of provider/onprem/onprem_instance.go per job; the tests of the package have to fail.
OLD_PAD = '\t\tif len(octets[i]) == 1 {\n\t\t\toctets[i] = "0" + octets[i]\n\t\t}'
M = {
    # the old behaviour: the zeros are dropped from both sides
    "zeros-dropped": (OLD_PAD, '\t\toctets[i] = strings.TrimLeft(octets[i], "0")'),
    # the case of the letters is kept
    "case-kept": ('strings.Split(strings.ToLower(mac), ":")', 'strings.Split(mac, ":")'),
    # a part of the mac is taken for the mac (the old behaviour looked for it inside the line)
    "substring-match": ("got == want", "strings.HasSuffix(got, want[1:])"),
}

name = sys.argv[1]
old, new = M[name]
path = "provider/onprem/onprem_instance.go"
s = open(path).read()
if s.count(old) != 1:
    sys.exit("%s: pattern found %d times" % (name, s.count(old)))
open(path, "w").write(s.replace(old, new))
r = subprocess.run(["go", "test", "./provider/onprem/...", "-count", "1"], capture_output=True, text=True)
out = r.stdout + r.stderr
print(out[-2000:])
if r.returncode == 0:
    sys.exit("SURVIVED: " + name)
if "[build failed]" in out:
    sys.exit("BROKEN MUTATION: " + name)
print("KILLED: " + name)
