// Command release builds the release archives of the wildcsv command, their
// checksums, the Homebrew and Scoop manifests and the release notes.
//
//	go run ./internal/cmd/release build --tag v0.1.0 --out dist
//	go run ./internal/cmd/release build --tag v0.0.0 --out dist --dry-run
//	go run ./internal/cmd/release packaging --tag v0.1.0 --checksums dist/checksums.txt --out packaging
//	go run ./internal/cmd/release notes --tag v0.1.0
//
// The same tag gives the same archives: binaries are built with -trimpath,
// which makes Go builds reproducible, and archive entries have fixed names,
// order, modes and timestamps.
package main

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	module = "github.com/Altitud-Studio-S-L-U/wildcsv"
	repo   = "https://github.com/Altitud-Studio-S-L-U/wildcsv"
	desc   = "Read delimited text of unknown dialect and encoding under explicit limits"
)

type target struct{ goos, goarch string }

var targets = []target{
	{"darwin", "amd64"}, {"darwin", "arm64"},
	{"linux", "amd64"}, {"linux", "arm64"},
	{"windows", "amd64"}, {"windows", "arm64"},
}

var tagPattern = regexp.MustCompile(`^v(\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?)$`)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "build":
		err = build(os.Args[2:])
	case "packaging":
		err = packaging(os.Args[2:])
	case "notes":
		err = notes(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "release:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: release build|packaging|notes --tag vX.Y.Z [flags]")
	os.Exit(2)
}

func version(tag string) (string, error) {
	m := tagPattern.FindStringSubmatch(tag)
	if m == nil {
		return "", fmt.Errorf("invalid tag %q", tag)
	}
	return m[1], nil
}

func archiveName(v string, t target) string {
	ext := ".tar.gz"
	if t.goos == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf("wildcsv_%s_%s_%s%s", v, t.goos, t.goarch, ext)
}

// commitTime is the time of HEAD, used for every archive entry.
func commitTime() (time.Time, error) {
	out, err := exec.Command("git", "log", "-1", "--format=%ct").Output()
	if err != nil {
		return time.Time{}, fmt.Errorf("git log: %w", err)
	}
	var sec int64
	if _, err := fmt.Sscan(strings.TrimSpace(string(out)), &sec); err != nil {
		return time.Time{}, err
	}
	return time.Unix(sec, 0).UTC(), nil
}

func build(args []string) error {
	fs := flag.NewFlagSet("build", flag.ExitOnError)
	tag := fs.String("tag", "", "release tag, such as v0.1.0")
	out := fs.String("out", "dist", "output directory")
	dry := fs.Bool("dry-run", false, "do not require the binaries to carry the tag's version")
	fs.Parse(args)
	v, err := version(*tag)
	if err != nil {
		return err
	}
	mtime, err := commitTime()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "wildcsv-release-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	license, err := os.ReadFile("LICENSE")
	if err != nil {
		return err
	}
	readme, err := os.ReadFile("README.md")
	if err != nil {
		return err
	}
	var sums []string
	for _, t := range targets {
		bin := "wildcsv"
		if t.goos == "windows" {
			bin += ".exe"
		}
		binPath := filepath.Join(work, t.goos+"-"+t.goarch, bin)
		cmd := exec.Command("go", "build", "-trimpath", "-o", binPath, "./cmd/wildcsv")
		cmd.Env = append(os.Environ(), "GOOS="+t.goos, "GOARCH="+t.goarch, "CGO_ENABLED=0")
		cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("build %s/%s: %w", t.goos, t.goarch, err)
		}
		info, err := buildinfo.ReadFile(binPath)
		if err != nil {
			return err
		}
		if info.Main.Path != module {
			return fmt.Errorf("%s/%s: main module is %q", t.goos, t.goarch, info.Main.Path)
		}
		if !*dry && info.Main.Version != *tag {
			return fmt.Errorf("%s/%s: the binary carries version %q, not %s; build from a clean checkout of the tag", t.goos, t.goarch, info.Main.Version, *tag)
		}
		binData, err := os.ReadFile(binPath)
		if err != nil {
			return err
		}
		files := []entry{{bin, binData, 0o755}, {"LICENSE", license, 0o644}, {"README.md", readme, 0o644}}
		var buf bytes.Buffer
		if t.goos == "windows" {
			err = writeZip(&buf, files, mtime)
		} else {
			err = writeTarGz(&buf, files, mtime)
		}
		if err != nil {
			return err
		}
		name := archiveName(v, t)
		if err := os.WriteFile(filepath.Join(*out, name), buf.Bytes(), 0o644); err != nil {
			return err
		}
		sum := sha256.Sum256(buf.Bytes())
		sums = append(sums, hex.EncodeToString(sum[:])+"  "+name)
		fmt.Fprintf(os.Stderr, "%s  %d bytes, version %s\n", name, buf.Len(), info.Main.Version)
	}
	return os.WriteFile(filepath.Join(*out, "checksums.txt"), []byte(strings.Join(sums, "\n")+"\n"), 0o644)
}

type entry struct {
	name string
	data []byte
	mode int64
}

func writeTarGz(w io.Writer, files []entry, mtime time.Time) error {
	gz, err := gzip.NewWriterLevel(w, gzip.BestCompression)
	if err != nil {
		return err
	}
	gz.ModTime = mtime
	tw := tar.NewWriter(gz)
	for _, f := range files {
		hdr := &tar.Header{Name: f.name, Mode: f.mode, Size: int64(len(f.data)), ModTime: mtime, Format: tar.FormatPAX, Uname: "root", Gname: "root"}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := tw.Write(f.data); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func writeZip(w io.Writer, files []entry, mtime time.Time) error {
	zw := zip.NewWriter(w)
	for _, f := range files {
		hdr := &zip.FileHeader{Name: f.name, Method: zip.Deflate, Modified: mtime}
		hdr.SetMode(os.FileMode(f.mode))
		fw, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		if _, err := fw.Write(f.data); err != nil {
			return err
		}
	}
	return zw.Close()
}

func readChecksums(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sums := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		parts := strings.Fields(sc.Text())
		if len(parts) == 2 {
			sums[parts[1]] = parts[0]
		}
	}
	return sums, sc.Err()
}

func packaging(args []string) error {
	fs := flag.NewFlagSet("packaging", flag.ExitOnError)
	tag := fs.String("tag", "", "release tag")
	checksums := fs.String("checksums", "dist/checksums.txt", "checksums written by build")
	out := fs.String("out", "packaging", "output directory")
	fs.Parse(args)
	v, err := version(*tag)
	if err != nil {
		return err
	}
	sums, err := readChecksums(*checksums)
	if err != nil {
		return err
	}
	url := func(t target) (string, string, error) {
		name := archiveName(v, t)
		sum, ok := sums[name]
		if !ok {
			return "", "", fmt.Errorf("%s is not in %s", name, *checksums)
		}
		return fmt.Sprintf("%s/releases/download/%s/%s", repo, *tag, name), sum, nil
	}

	var rb strings.Builder
	fmt.Fprintf(&rb, "class Wildcsv < Formula\n  desc %q\n  homepage %q\n  version %q\n  license \"MIT\"\n", desc, repo, v)
	for _, osName := range []string{"darwin", "linux"} {
		block := map[string]string{"darwin": "on_macos", "linux": "on_linux"}[osName]
		fmt.Fprintf(&rb, "\n  %s do\n", block)
		for _, arch := range []string{"arm64", "amd64"} {
			u, sum, err := url(target{osName, arch})
			if err != nil {
				return err
			}
			cpu := map[string]string{"arm64": "on_arm", "amd64": "on_intel"}[arch]
			fmt.Fprintf(&rb, "    %s do\n      url %q\n      sha256 %q\n    end\n", cpu, u, sum)
		}
		rb.WriteString("  end\n")
	}
	rb.WriteString("\n  def install\n    bin.install \"wildcsv\"\n  end\n\n  test do\n")
	rb.WriteString("    (testpath/\"t.csv\").write(\"a;b\\n1;2\\n\")\n")
	rb.WriteString("    assert_match \"semicolon\", shell_output(\"#{bin}/wildcsv inspect #{testpath}/t.csv\")\n  end\nend\n")

	u64, s64, err := url(target{"windows", "amd64"})
	if err != nil {
		return err
	}
	uArm, sArm, err := url(target{"windows", "arm64"})
	if err != nil {
		return err
	}
	auto := func(arch string) string {
		return repo + "/releases/download/v$version/wildcsv_$version_windows_" + arch + ".zip"
	}
	scoop := map[string]any{
		"version":     v,
		"description": desc + ".",
		"homepage":    repo,
		"license":     "MIT",
		"architecture": map[string]any{
			"64bit": map[string]string{"url": u64, "hash": s64},
			"arm64": map[string]string{"url": uArm, "hash": sArm},
		},
		"bin":      "wildcsv.exe",
		"checkver": map[string]string{"github": repo},
		"autoupdate": map[string]any{
			"architecture": map[string]any{
				"64bit": map[string]string{"url": auto("amd64")},
				"arm64": map[string]string{"url": auto("arm64")},
			},
			"hash": map[string]string{"url": "$baseurl/checksums.txt"},
		},
	}
	sj, err := json.MarshalIndent(scoop, "", "  ")
	if err != nil {
		return err
	}
	for path, data := range map[string][]byte{
		filepath.Join(*out, "Formula", "wildcsv.rb"):  []byte(rb.String()),
		filepath.Join(*out, "bucket", "wildcsv.json"): append(sj, '\n'),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// notes prints the CHANGELOG section of the tag's version.
func notes(args []string) error {
	fs := flag.NewFlagSet("notes", flag.ExitOnError)
	tag := fs.String("tag", "", "release tag")
	fs.Parse(args)
	v, err := version(*tag)
	if err != nil {
		return err
	}
	data, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		return err
	}
	var section []string
	in := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "## ") {
			if in {
				break
			}
			in = strings.HasPrefix(line, "## ["+v+"]")
			continue
		}
		if in {
			section = append(section, line)
		}
	}
	text := strings.TrimSpace(strings.Join(section, "\n"))
	if text == "" {
		return errors.New("CHANGELOG.md has no section for " + v)
	}
	fmt.Println(text)
	return nil
}
