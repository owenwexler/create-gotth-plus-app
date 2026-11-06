package prereq

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

// fake is a Runner backed by maps: present commands, and outputs keyed by "name arg arg".
type fake struct {
	present map[string]bool
	outputs map[string]string
}

func (f fake) runner() Runner {
	return Runner{
		LookPath: func(file string) (string, error) {
			if f.present[file] {
				return "/usr/bin/" + file, nil
			}
			return "", errors.New("not found")
		},
		Output: func(_ context.Context, name string, args ...string) ([]byte, error) {
			out, ok := f.outputs[strings.Join(append([]string{name}, args...), " ")]
			if !ok {
				return nil, errors.New("unexpected command")
			}
			return []byte(out + "\n"), nil
		},
	}
}

func allGood() fake {
	return fake{
		present: map[string]bool{"go": true, "templ": true, "tailwindcss": true, "make": true, "bash": true, "git": true, "curl": true, "openssl": true, "gcc": true},
		outputs: map[string]string{
			"go env GOVERSION":   "go1.27.3",
			"go env CGO_ENABLED": "1",
			"go env CC":          "gcc",
			"templ version":      "v0.3.1070",
			"tailwindcss --help": "≈ tailwindcss v4.3.3\n\nUsage:",
		},
	}
}

var req = Requirements{Go: "1.27.3", Templ: "v0.3.1070"}

func check(f fake, needs Needs) map[string]Result {
	byName := map[string]Result{}
	for _, res := range f.runner().Check(context.Background(), Tools(req, needs)) {
		byName[res.Tool.Name] = res
	}
	return byName
}

func TestAllGood(t *testing.T) {
	results := check(allGood(), Needs{CCompiler: true})
	for name, res := range results {
		if res.Err != nil {
			t.Errorf("%s: %v", name, res.Err)
		}
	}
	if _, ok := results["C compiler"]; !ok {
		t.Error("C compiler not checked although needed")
	}
	if _, ok := check(allGood(), Needs{})["C compiler"]; ok {
		t.Error("C compiler checked although not needed")
	}

	var buf bytes.Buffer
	if !Report(&buf, allGood().runner().Check(context.Background(), Tools(req, Needs{}))) {
		t.Error("Report = false")
	}
	if !strings.Contains(buf.String(), "all found") || !strings.Contains(buf.String(), "Go go1.27.3") {
		t.Errorf("report: %s", buf.String())
	}
}

func TestProblems(t *testing.T) {
	tests := []struct {
		name  string
		tool  string
		edit  func(f fake)
		want  string
		needs Needs
	}{
		{"go too old", "Go", func(f fake) { f.outputs["go env GOVERSION"] = "go1.27.2" }, "1.27.3 or newer", Needs{}},
		{"go missing", "Go", func(f fake) { delete(f.present, "go") }, "not found", Needs{}},
		{"templ mismatch", "templ", func(f fake) { f.outputs["templ version"] = "v0.3.1020" }, "v0.3.1070", Needs{}},
		{"tailwind v3", "Tailwind CSS v4 standalone CLI (tailwindcss)", func(f fake) { f.outputs["tailwindcss --help"] = "tailwindcss v3.4.1" }, "needs Tailwind CSS v4", Needs{}},
		{"tailwind unreadable", "Tailwind CSS v4 standalone CLI (tailwindcss)", func(f fake) { f.outputs["tailwindcss --help"] = "huh" }, "could not read", Needs{}},
		{"openssl missing", "openssl", func(f fake) { delete(f.present, "openssl") }, "not found", Needs{}},
		{"cgo off", "C compiler", func(f fake) { f.outputs["go env CGO_ENABLED"] = "0" }, "CGO is turned off", Needs{CCompiler: true}},
		{"cc missing", "C compiler", func(f fake) { delete(f.present, "gcc") }, "gcc (go env CC) not found", Needs{CCompiler: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := allGood()
			tt.edit(f)
			res, ok := check(f, tt.needs)[tt.tool]
			if !ok {
				t.Fatalf("%s not checked", tt.tool)
			}
			if res.Err == nil || !strings.Contains(res.Err.Error(), tt.want) {
				t.Errorf("err = %v, want it to contain %q", res.Err, tt.want)
			}
		})
	}
}

func TestDevelGoIsAccepted(t *testing.T) {
	f := allGood()
	f.outputs["go env GOVERSION"] = "devel go1.28-abcdef"
	if res := check(f, Needs{})["Go"]; res.Err != nil {
		t.Errorf("devel Go rejected: %v", res.Err)
	}
}

func TestReportShowsInstallSteps(t *testing.T) {
	f := allGood()
	delete(f.present, "tailwindcss")
	var buf bytes.Buffer
	if Report(&buf, f.runner().Check(context.Background(), Tools(req, Needs{}))) {
		t.Error("Report = true with a missing tool")
	}
	out := buf.String()
	for _, want := range []string{"problem  Tailwind", "Install: https://github.com/tailwindlabs/tailwindcss/releases", "can still be created", "ok       Go go1.27.3"} {
		if !strings.Contains(out, want) {
			t.Errorf("report is missing %q:\n%s", want, out)
		}
	}
}
