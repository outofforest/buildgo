package buildgo

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/outofforest/build"
	"github.com/outofforest/libexec"
	"github.com/outofforest/logger"
	"github.com/pkg/errors"
	"github.com/ridge/must"
	"go.uber.org/zap"
)

// GoBuildPkg builds go package
func GoBuildPkg(ctx context.Context, pkg, out string, cgo bool, tags ...string) error {
	logger.Get(ctx).Info("Building go package", zap.String("package", pkg), zap.String("binary", out))

	args := []string{
		"build",
		"-trimpath",
		"-ldflags=-w -s",
		"-o", must.String(filepath.Abs(out)),
	}
	if len(tags) > 0 {
		args = append(args, "-tags", strings.Join(tags, ","))
	}

	cmd := exec.Command("go", append(args, ".")...)
	cmd.Dir = pkg
	if !cgo {
		cmd.Env = append([]string{"CGO_ENABLED=0"}, os.Environ()...)
	}
	if err := libexec.Exec(ctx, cmd); err != nil {
		return errors.Wrapf(err, "building go package '%s' failed", pkg)
	}
	return nil
}

// GoLint runs golangci linter, runs go mod tidy and checks that git tree is clean
func GoLint(ctx context.Context, deps build.DepsFunc) error {
	deps(EnsureGo, EnsureGolangCI)
	log := logger.Get(ctx)
	config := must.String(filepath.Abs("build/.golangci.yaml"))
	err := onModule(func(path string) error {
		log.Info("Running linter", zap.String("path", path))
		cmd := exec.Command("golangci-lint", "run", "--config", config)
		cmd.Dir = path
		if err := libexec.Exec(ctx, cmd); err != nil {
			return errors.Wrapf(err, "linter errors found in module '%s'", path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	deps(GoModTidy, gitStatusClean)
	return nil
}

// GoTest runs go test
func GoTest(ctx context.Context, deps build.DepsFunc, tags ...string) error {
	deps(EnsureGo)
	log := logger.Get(ctx)

	rootDir := must.String(filepath.EvalSymlinks(must.String(filepath.Abs(".."))))
	repoDir := must.String(filepath.EvalSymlinks(must.String(filepath.Abs("."))))
	coverageDir := filepath.Join(repoDir, "bin", ".coverage")
	if err := os.MkdirAll(coverageDir, 0o700); err != nil {
		return errors.WithStack(err)
	}

	return onModule(func(path string) error {
		relPath, err := filepath.Rel(rootDir, must.String(filepath.EvalSymlinks(must.String(filepath.Abs(path)))))
		if err != nil {
			return errors.WithStack(err)
		}

		args := []string{
			"test",
			"-count=1",
			"-shuffle=on",
			"-race",
			"-cover", "./...",
			"-coverpkg", "./...",
			"-coverprofile", filepath.Join(coverageDir, strings.ReplaceAll(relPath, "/", "-")),
		}
		if len(tags) > 0 {
			args = append(args, "-tags", strings.Join(tags, ","))
		}

		log.Info("Running go tests", zap.String("path", path))
		cmd := exec.Command("go", append(args, "./...")...)
		cmd.Dir = path
		if err := libexec.Exec(ctx, cmd); err != nil {
			return errors.Wrapf(err, "unit tests failed in module '%s'", path)
		}
		return nil
	})
}

// GoModTidy calls `go mod tidy`
func GoModTidy(ctx context.Context, deps build.DepsFunc) error {
	deps(EnsureGo)
	log := logger.Get(ctx)
	return onModule(func(path string) error {
		log.Info("Running go mod tidy", zap.String("path", path))
		cmd := exec.Command("go", "mod", "tidy")
		cmd.Dir = path
		if err := libexec.Exec(ctx, cmd); err != nil {
			return errors.Wrapf(err, "'go mod tidy' failed in module '%s'", path)
		}
		return nil
	})
}

func rebuildMe(ctx context.Context, deps build.DepsFunc) error {
	deps(EnsureGo)
	return GoBuildPkg(ctx, "build/cmd", must.String(filepath.EvalSymlinks(must.String(os.Executable()))), false)
}

func onModule(fn func(path string) error) error {
	return filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if d.IsDir() || d.Name() != "go.mod" {
			return nil
		}
		return fn(filepath.Dir(path))
	})
}
