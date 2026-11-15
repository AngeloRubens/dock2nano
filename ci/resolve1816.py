# test only: resolves the two conflict hunks of cherry-picking the dns_search commit onto the PR 1816 head
# (keep the PR's fresh config, add the search argument of the dns_search commit)
import sys
p = sys.argv[1]
s = open(p).read()
a = """<<<<<<< HEAD
		pid, err := com.spawnProgram(y.Packages[i], dnsIP, brName)
=======
		pid, err := com.spawnProgram(y.Packages[i], dnsIP, y.DNSSearch, com.config, brName)
>>>>>>> f3ee91e (compose: dns_search puts search domains in the resolv.conf of the unikernels)
"""
b = """<<<<<<< HEAD
func (com Compose) spawnProgram(comp ComposePackage, dnsIP string, brName string) (string, error) {
	c := api.NewConfig()
	version := api.LocalReleaseVersion
	c.Boot = path.Join(api.GetOpsHome(), version, "boot.img")
=======
func (com Compose) spawnProgram(comp ComposePackage, dnsIP string, search []string, c *types.Config, brName string) (string, error) {
>>>>>>> f3ee91e (compose: dns_search puts search domains in the resolv.conf of the unikernels)
"""
assert a in s and b in s, "conflict hunks differ from the expected ones"
s = s.replace(a, "\t\tpid, err := com.spawnProgram(y.Packages[i], dnsIP, y.DNSSearch, brName)\n")
s = s.replace(b, """func (com Compose) spawnProgram(comp ComposePackage, dnsIP string, search []string, brName string) (string, error) {
	c := api.NewConfig()
	version := api.LocalReleaseVersion
	c.Boot = path.Join(api.GetOpsHome(), version, "boot.img")
""")
assert "<<<<<<<" not in s
open(p, "w").write(s)
