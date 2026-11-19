package compose

import (
	"reflect"
	"strings"
	"testing"
)

func TestSecretFileBecomesAReadOnlyBindAtRunSecrets(t *testing.T) {
	p := mustParse(t, `
services:
  db:
    image: mariadb
    secrets:
      - db-password
secrets:
  db-password:
    file: db/password.txt
`)
	db := p.Service("db")
	want := []Volume{{Type: "bind", Source: "db/password.txt", Target: "/run/secrets/db-password", ReadOnly: true, Secret: true}}
	if !reflect.DeepEqual(db.Volumes, want) {
		t.Fatalf("volumes = %+v", db.Volumes)
	}
	if len(db.Unknown) != 0 || len(p.Unknown) != 0 {
		t.Fatalf("unknown = %v %v", db.Unknown, p.Unknown)
	}
}

func TestSecretLongFormHasATarget(t *testing.T) {
	p := mustParse(t, `
services:
  web:
    image: x
    secrets:
      - source: key
        target: tls.key
      - source: key
        target: /etc/ssl/key.pem
secrets:
  key:
    file: ./key.pem
`)
	v := p.Service("web").Volumes
	if len(v) != 2 || v[0].Target != "/run/secrets/tls.key" || v[1].Target != "/etc/ssl/key.pem" {
		t.Fatalf("volumes = %+v", v)
	}
}

func TestSecretThatIsNotAFileIsListedAndNotMounted(t *testing.T) {
	p := mustParse(t, `
services:
  web:
    image: x
    secrets: [token, missing]
secrets:
  token:
    environment: TOKEN
`)
	web := p.Service("web")
	if len(web.Volumes) != 0 {
		t.Fatalf("volumes = %+v", web.Volumes)
	}
	if !reflect.DeepEqual(web.Unknown, []string{"secrets.token", "secrets.missing"}) {
		t.Fatalf("service unknown = %v", web.Unknown)
	}
	if !reflect.DeepEqual(p.Unknown, []string{"secrets.token.environment"}) {
		t.Fatalf("project unknown = %v", p.Unknown)
	}
}

func TestSecretModeAndOwnerAreListed(t *testing.T) {
	p := mustParse(t, `
services:
  web:
    image: x
    secrets:
      - source: s
        mode: 0400
secrets:
  s:
    file: s.txt
`)
	web := p.Service("web")
	if len(web.Volumes) != 1 || !strings.Contains(strings.Join(web.Unknown, ","), "secrets.mode") {
		t.Fatalf("volumes = %+v unknown = %v", web.Volumes, web.Unknown)
	}
}
