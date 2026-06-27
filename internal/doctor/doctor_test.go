package doctor

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/chmouel/rc/internal/config"
	"github.com/chmouel/rc/internal/output"
	"github.com/chmouel/rc/internal/runner"
)

func testCfg(t *testing.T) *config.Config {
	t.Helper()
	vars := config.Vars{"HOME": t.TempDir(), "HOST": "ibra", "GOPATH": "/go"}
	cfg, err := config.Build([]config.File{config.Defaults()}, vars, "ibra")
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestDoctorOfflineSkipsNetwork(t *testing.T) {
	fake := runner.NewFake()
	d := &Doctor{
		R:       fake,
		Rep:     output.New(io.Discard, io.Discard, false, false),
		Cfg:     testCfg(t),
		Offline: true,
	}
	sum, _ := d.Run(context.Background())
	var sawNetSkip bool
	for _, r := range d.results {
		if r.Name == "connectivity" && r.Status == Skip {
			sawNetSkip = true
		}
		if r.Name == "github-auth" && r.Status != Skip {
			t.Error("github-auth should be skipped offline")
		}
	}
	if !sawNetSkip {
		t.Error("connectivity should be skipped offline")
	}
	if sum.Pass+sum.Warn+sum.Fail+sum.Info+sum.Skip == 0 {
		t.Error("doctor should always reach a non-empty summary")
	}
}

func TestDoctorMissingGitFails(t *testing.T) {
	fake := runner.NewFake()
	fake.Missing["git"] = true
	d := &Doctor{
		R:       fake,
		Rep:     output.New(io.Discard, io.Discard, false, false),
		Cfg:     testCfg(t),
		Offline: true,
	}
	sum, err := d.Run(context.Background())
	if err == nil {
		t.Fatal("missing git should cause a failure")
	}
	if sum.Fail == 0 {
		t.Error("expected at least one failure counted")
	}
}

func TestDoctorAlwaysReachesSummary(t *testing.T) {
	// Even with everything missing, Run must complete and count results.
	fake := runner.NewFake()
	fake.Missing["git"] = true
	fake.Missing["yadm"] = true
	d := &Doctor{
		R:       fake,
		Rep:     output.New(io.Discard, io.Discard, false, false),
		Cfg:     testCfg(t),
		Offline: true,
	}
	sum, _ := d.Run(context.Background())
	if len(d.results) == 0 {
		t.Fatal("no checks ran")
	}
	total := sum.Pass + sum.Warn + sum.Fail + sum.Info + sum.Skip
	if total != len(d.results) {
		t.Errorf("summary count %d != results %d", total, len(d.results))
	}
}

func TestDoctorDiagnosticsStayOffStdout(t *testing.T) {
	fake := runner.NewFake()
	var out, errBuf bytes.Buffer
	d := &Doctor{
		R:       fake,
		Rep:     output.New(&out, &errBuf, false, false),
		Cfg:     testCfg(t),
		Offline: true,
	}
	if _, err := d.Run(context.Background()); err != nil {
		t.Fatalf("doctor should pass in offline fake environment: %v", err)
	}
	if out.String() != "" {
		t.Fatalf("doctor diagnostics should not use stdout, got %q", out.String())
	}
	if !strings.Contains(errBuf.String(), "Summary:") {
		t.Fatalf("stderr missing summary: %q", errBuf.String())
	}
}
