package provider

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/config"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/TrueNorthIT/terraform-provider-dataversecontact/internal/fakeapi"
)

// Whole configurations, applied as written: the examples users copy, and
// (locally) our own portal configs. Each must apply, plan clean afterwards,
// and destroy to nothing.

// TestAccExamples applies every example under examples/ that declares
// resources.
func TestAccExamples(t *testing.T) {
	dirs := []string{
		"../../examples/full-scope",
		"../../examples/resources/dataversecontact_table",
		"../../examples/resources/dataversecontact_custom_api",
		"../../examples/resources/dataversecontact_permissions_sync",
	}
	for _, dir := range dirs {
		t.Run(filepath.Base(dir), func(t *testing.T) { applyConfigDir(t, dir) })
	}
}

// TestAccRealConfigs applies real portal configs from outside this repo. They
// are private, so the test only runs when DATAVERSE_TF_CONFIG_DIRS lists
// their directories (separated by the OS path-list separator).
func TestAccRealConfigs(t *testing.T) {
	list := os.Getenv("DATAVERSE_TF_CONFIG_DIRS")
	if list == "" {
		t.Skip("set DATAVERSE_TF_CONFIG_DIRS to run real portal configs against the fake API")
	}
	for _, dir := range filepath.SplitList(list) {
		t.Run(filepath.Base(dir), func(t *testing.T) { applyConfigDir(t, dir) })
	}
}

func applyConfigDir(t *testing.T, dir string) {
	s := fakeapi.New(t)
	work := copyConfigDir(t, dir)
	vars := config.Variables{
		"api_url":        config.StringVariable(s.URL),
		"connection_key": config.StringVariable(fakeapi.Key),
	}
	// Configs with no provider block of their own take the fake from here.
	t.Setenv("DATAVERSE_CONTACT_API_URL", s.URL)
	t.Setenv("DATAVERSE_CONTACT_CONNECTION_KEY", fakeapi.Key)
	resource.UnitTest(t, resource.TestCase{
		CheckDestroy: func(*terraform.State) error {
			for _, scope := range scopesIn(s) {
				if n := s.TableCount(scope) + s.ApiCount(scope); n != 0 {
					return fmt.Errorf("scope %s: %d table(s)/custom API(s) left after destroy", scope, n)
				}
			}
			return nil
		},
		// On the step, not the case: plugin-testing counts a provider block in
		// a config directory as step-level providers, and refuses both.
		Steps: []resource.TestStep{{
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			ConfigDirectory:          config.StaticDirectory(work),
			ConfigVariables:          declaredOnly(t, work, vars),
		}},
	})
}

// copyConfigDir copies a config (with any data files it reads, such as
// custom API schemas) into a temp dir, without its terraform {} block, so the
// provider resolves to the one under test rather than the registry release the
// config pins. Dot-directories, state and variable files are left behind.
func copyConfigDir(t *testing.T, dir string) string {
	t.Helper()
	work := t.TempDir()
	err := filepath.WalkDir(dir, func(path string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") && path != dir {
			if e.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		if e.IsDir() {
			return os.MkdirAll(filepath.Join(work, rel), 0o700)
		}
		if strings.HasSuffix(name, ".tfstate") || strings.HasSuffix(name, ".tfvars") ||
			strings.HasSuffix(name, ".hcl") || strings.Contains(name, ".tfstate.") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.HasSuffix(name, ".tf") {
			b = []byte(stripBlock(string(b), "terraform"))
			// plugin-testing runs a copy of the top level only, so data files
			// in subdirectories are read from here instead.
			b = []byte(strings.ReplaceAll(string(b), "${path.module}", filepath.ToSlash(work)))
		}
		return os.WriteFile(filepath.Join(work, rel), b, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	return work
}

// stripBlock removes every top-level `<name> {...}` block.
func stripBlock(src, name string) string {
	var out strings.Builder
	lines := strings.SplitAfter(src, "\n")
	for i := 0; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), name+" {") && !strings.HasPrefix(lines[i], " ") {
			depth := 0
			for ; i < len(lines); i++ {
				depth += strings.Count(lines[i], "{") - strings.Count(lines[i], "}")
				if depth <= 0 {
					break
				}
			}
			continue
		}
		out.WriteString(lines[i])
	}
	return out.String()
}

// declaredOnly drops the variables a config doesn't declare: Terraform
// rejects values for undeclared variables.
func declaredOnly(t *testing.T, dir string, vars config.Variables) config.Variables {
	t.Helper()
	var src strings.Builder
	files, _ := filepath.Glob(filepath.Join(dir, "*.tf"))
	for _, f := range files {
		b, _ := os.ReadFile(f)
		src.Write(b)
	}
	out := config.Variables{}
	for name, v := range vars {
		if strings.Contains(src.String(), fmt.Sprintf("variable %q", name)) {
			out[name] = v
		}
	}
	return out
}

func scopesIn(s *fakeapi.Server) []string {
	seen := map[string]bool{}
	for _, r := range s.Requests() {
		rest, ok := strings.CutPrefix(strings.SplitN(r, " ", 2)[1], "/api/v2/_admin/")
		if !ok || rest == "scopes" {
			continue
		}
		seen[strings.SplitN(rest, "/", 2)[0]] = true
	}
	scopes := make([]string, 0, len(seen))
	for scope := range seen {
		scopes = append(scopes, scope)
	}
	return scopes
}
