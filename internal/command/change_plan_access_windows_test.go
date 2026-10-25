package command_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestBuiltCLIChangePlanRejectsNativeAccessDrift(t *testing.T) {
	binary := buildSourceIntegrityCLI(t)
	wrapper := nativeAccessGoWrapper(t)
	for _, kind := range []string{"project", "goenv", "unchanged"} {
		t.Run(kind, func(t *testing.T) {
			root := writeImplementationSelectionCommandProject(t)
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "interfaces:\n  require: [reports.read/v1]\n")
			path := filepath.Join(root, "plystra.yaml")
			overrides := map[string]string{}
			if kind == "goenv" {
				path = filepath.Join(t.TempDir(), "env")
				writeCommandFile(t, path, "GOPRIVATE=example.com/private\n")
				overrides["GOENV"] = path
			}
			preserveCommandAccess(t, path)
			before := commandTree(t, root)
			beforeInfo, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(t.TempDir(), "access-changed")
			overrides["PLYSTRA_ACCESS_PATH"] = path
			overrides["PLYSTRA_ACCESS_MARKER"] = marker
			overrides["PLYSTRA_ACCESS_MODE"] = kind
			environment := implementationSelectionCommandEnvironment(overrides)
			for index, entry := range environment {
				if name, value, _ := strings.Cut(entry, "="); strings.EqualFold(name, "PATH") {
					environment[index] = name + "=" + filepath.Dir(wrapper) + string(os.PathListSeparator) + value
				}
			}
			command := exec.CommandContext(t.Context(), binary, "change", "plan", "--request", "-", "--format", "json")
			command.Dir, command.Env = root, environment
			command.Stdin = strings.NewReader(`{"schema":"plystra.change/v1","operations":[{"kind":"interface_create","name":"todo.add","semantics":"command"}]}`)
			var stderr bytes.Buffer
			command.Stderr = &stderr
			output, err := command.Output()
			var result scaffoldPlanDocument
			decodeErr := json.Unmarshal(output, &result)
			if kind == "unchanged" {
				if err != nil || decodeErr != nil || result.Status != "success" || result.Payload.Completeness != "non_exact" || result.Payload.PlanDigest == "" || stderr.Len() != 0 {
					t.Fatalf("unchanged native permissions: %v, decode=%v, status=%s, stderr-bytes=%d", err, decodeErr, result.Status, stderr.Len())
				}
				if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("unchanged preview mutated permissions")
				}
			} else {
				if data, markerErr := os.ReadFile(marker); markerErr != nil || string(data) != "ACL changed\n" {
					t.Fatalf("native permission race was not exercised: %v", markerErr)
				}
				var exited *exec.ExitError
				if !errors.As(err, &exited) || exited.ExitCode() != 3 || decodeErr != nil || result.Status != "validation_failed" || result.Payload.PlanDigest != "" || stderr.Len() != 0 {
					t.Fatalf("native permission drift accepted: %v, decode=%v, status=%s, stderr-bytes=%d", err, decodeErr, result.Status, stderr.Len())
				}
			}
			afterInfo, statErr := os.Stat(path)
			if statErr != nil || !os.SameFile(beforeInfo, afterInfo) || beforeInfo.Mode() != afterInfo.Mode() || !beforeInfo.ModTime().Equal(afterInfo.ModTime()) {
				t.Fatal("ACL fixture changed ordinary file metadata")
			}
			for _, private := range []string{root, path, "O:S-1-", "D:P(", "S-1-5-21-"} {
				for _, spelling := range []string{private, filepath.ToSlash(private)} {
					encoded, _ := json.Marshal(spelling)
					if bytes.Contains(output, encoded[1:len(encoded)-1]) {
						t.Fatal("native permission evidence escaped to public JSON")
					}
				}
			}
			if !reflect.DeepEqual(before, commandTree(t, root)) {
				t.Fatal("preview changed authored bytes")
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func preserveCommandAccess(t *testing.T, path string) {
	t.Helper()
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := descriptor.Control()
	if err != nil {
		t.Fatal(err)
	}
	flags := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION | windows.UNPROTECTED_DACL_SECURITY_INFORMATION)
	if control&windows.SE_DACL_PROTECTED != 0 {
		flags = windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION
	}
	t.Cleanup(func() {
		if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, flags, nil, nil, dacl, nil); err != nil {
			t.Error(err)
		}
	})
}

func nativeAccessGoWrapper(t *testing.T) string {
	t.Helper()
	realGo, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	source := `package main
import (
 "os"
 "os/exec"
 "golang.org/x/sys/windows"
)
func check(err error) { if err != nil { os.Exit(9) } }
func main() {
 command := exec.Command(REAL_GO, os.Args[1:]...)
 command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
 err := command.Run()
 if err == nil && len(os.Args) > 2 && os.Args[1] == "list" && os.Args[2] == "-m" && os.Getenv("PLYSTRA_ACCESS_MODE") != "unchanged" {
  marker := os.Getenv("PLYSTRA_ACCESS_MARKER")
  if _, markerErr := os.Stat(marker); os.IsNotExist(markerErr) {
   user, userErr := windows.GetCurrentProcessToken().GetTokenUser(); check(userErr)
   descriptor, descriptorErr := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.User.Sid.String() + ")(A;;FR;;;WD)"); check(descriptorErr)
   dacl, _, aclErr := descriptor.DACL(); check(aclErr)
   check(windows.SetNamedSecurityInfo(os.Getenv("PLYSTRA_ACCESS_PATH"), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil))
   check(os.WriteFile(marker, []byte("ACL changed\n"), 0600))
  } else { check(markerErr) }
 }
 if exit, ok := err.(*exec.ExitError); ok { os.Exit(exit.ExitCode()) }; check(err)
}
`
	source = strings.ReplaceAll(source, "REAL_GO", fmt.Sprintf("%q", realGo))
	path := filepath.Join(root, "main.go")
	writeCommandFile(t, path, source)
	binary := filepath.Join(root, "go.exe")
	build := exec.CommandContext(t.Context(), realGo, "build", "-buildvcs=false", "-o", binary, path)
	build.Dir, build.Env = commandRepositoryRoot(t), commandGoEnvironment()
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build native access wrapper: %v\n%s", err, output)
	}
	return binary
}
