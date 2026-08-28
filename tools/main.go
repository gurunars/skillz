// Container entrypoint for the skillz repo: check, package, validate, and
// publish the agent skills. Runs inside the image built from Containerfile
// with the repo mounted rw at /work.
package main

import (
	"archive/zip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const dist = "dist"

func main() {
	if len(os.Args) < 2 {
		fatal("usage: skillz {check|build|validate|publish <tag>|clean}")
	}
	var err error
	switch os.Args[1] {
	case "check":
		err = check()
	case "build":
		err = build()
	case "validate":
		err = validate()
	case "publish":
		if len(os.Args) < 3 {
			fatal("usage: skillz publish <tag>   (e.g. v1.0.0)")
		}
		err = publish(os.Args[2])
	case "clean":
		err = os.RemoveAll(dist)
	default:
		fatal("unknown command %q", os.Args[1])
	}
	if err != nil {
		fatal("%v", err)
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

// skills returns every top-level directory containing a SKILL.md.
func skills() ([]string, error) {
	matches, err := filepath.Glob("*/SKILL.md")
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("no skills found (no */SKILL.md)")
	}
	dirs := make([]string, 0, len(matches))
	for _, m := range matches {
		dirs = append(dirs, filepath.Dir(m))
	}
	sort.Strings(dirs)
	return dirs, nil
}

// check is a cheap frontmatter sanity check; full agentskills.io spec
// validation is the validate command.
func check() error {
	dirs, err := skills()
	if err != nil {
		return err
	}
	for _, s := range dirs {
		data, err := os.ReadFile(filepath.Join(s, "SKILL.md"))
		if err != nil {
			return err
		}
		for _, field := range []string{"name:", "description:"} {
			if !hasFrontmatterField(string(data), field) {
				return fmt.Errorf("%s: SKILL.md missing %q frontmatter", s, strings.TrimSuffix(field, ":"))
			}
		}
		fmt.Printf("%s: ok\n", s)
	}
	return nil
}

func hasFrontmatterField(content, prefix string) bool {
	for line := range strings.SplitSeq(content, "\n") {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

// build packages each skill as dist/<name>.skill — a zip with the skill's
// directory at the archive root and SKILL.md inside it, the bundle format
// claude.ai accepts for manual upload.
func build() error {
	dirs, err := skills()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dist, 0o755); err != nil {
		return err
	}
	for _, s := range dirs {
		out := filepath.Join(dist, s+".skill")
		if err := zipDir(out, s); err != nil {
			return fmt.Errorf("%s: %w", s, err)
		}
		fmt.Printf("built %s\n", out)
	}
	return nil
}

func zipDir(out, dir string) (err error) {
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()
	w := zip.NewWriter(f)
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || d.Name() == ".DS_Store" {
			return nil
		}
		dst, err := w.Create(filepath.ToSlash(path))
		if err != nil {
			return err
		}
		src, err := os.Open(path)
		if err != nil {
			return err
		}
		defer src.Close()
		_, err = io.Copy(dst, src)
		return err
	})
	if err != nil {
		return err
	}
	return w.Close()
}

// validate checks every skill against the agentskills.io spec without
// publishing. gh requires auth even for a dry run; CI supplies the built-in
// workflow token.
func validate() error {
	if os.Getenv("GH_TOKEN") == "" {
		return fmt.Errorf("GH_TOKEN must be set (gh requires auth even for --dry-run; in CI the workflow token is used)")
	}
	return run("gh", "skill", "publish", "--dry-run")
}

// publish validates and publishes every skill as a GitHub release on this
// repo (gh skill publish), then attaches the .skill bundles to the release.
func publish(tag string) error {
	if os.Getenv("GH_TOKEN") == "" {
		return fmt.Errorf("GH_TOKEN must be set")
	}
	if err := build(); err != nil {
		return err
	}
	if err := run("gh", "skill", "publish", "--tag", tag); err != nil {
		return err
	}
	bundles, err := filepath.Glob(dist + "/*.skill")
	if err != nil {
		return err
	}
	args := append([]string{"release", "upload", tag}, bundles...)
	args = append(args, "--clobber")
	return run("gh", args...)
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
