package docker

import "testing"

// The repository IsoTTY checks against the pin must be the one Docker
// goes to, so only names Docker reads one way are taken: registry spelled
// out, tag given.
func TestRepoOfTakesOnlyNamesDockerReadsOneWay(t *testing.T) {
	for ref, want := range map[string]string{
		"registry.example.org/team/app:1.2":      "registry.example.org/team/app",
		"docker.io/library/alpine:3.20":          "docker.io/library/alpine",
		"registry.internal:5000/a/b/c:v1":        "registry.internal:5000/a/b/c",
		"localhost/app:dev":                      "localhost/app",
		"127.0.0.1:5000/e2e/probe:1":             "127.0.0.1:5000/e2e/probe",
		"ghcr.io/owner/some_image-name.x:latest": "ghcr.io/owner/some_image-name.x",
	} {
		got, err := RepoOf(ref)
		if err != nil || got != want {
			t.Errorf("RepoOf(%q) = %q, %v, want %q", ref, got, err, want)
		}
		if err := ValidateRepo(want); err != nil {
			t.Errorf("ValidateRepo(%q) = %v", want, err)
		}
	}
	for _, ref := range []string{
		"alpine:3.20",                   // Docker Hub by Docker's own rule
		"library/alpine:3.20",           // a namespace, not a registry
		"registry.example.org/team/app", // the tag would default
		"registry.example.org/team/app@sha256:0000000000000000000000000000000000000000000000000000000000000000",
		"registry.example.org/Team/app:1",   // not a name Docker takes
		"registry.example.org/team/app:1 x", // more than a reference
		"--platform=x/y:1",
		"registry.example.org/:1",
		"",
	} {
		if repo, err := RepoOf(ref); err == nil {
			t.Errorf("RepoOf(%q) = %q, want it refused", ref, repo)
		}
	}
	for _, repo := range []string{"alpine", "library/alpine", "registry.example.org/team/app:1.2", "registry.example.org"} {
		if err := ValidateRepo(repo); err == nil {
			t.Errorf("ValidateRepo(%q) accepted", repo)
		}
	}
}
