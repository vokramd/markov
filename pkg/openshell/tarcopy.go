package openshell

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

// writeTar writes src (a file or a directory) to w as a tar archive whose top entry is src's
// base name. With gitIgnore and src in a Git work tree, it holds only the files Git does not
// ignore, as `openshell sandbox upload` does.
func writeTar(w io.Writer, src string, gitIgnore bool) error {
	src = filepath.Clean(src)
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	keep, err := gitKept(src, info, gitIgnore)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(w)
	base := filepath.Base(src)
	err = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		rel = filepath.ToSlash(rel)
		if keep != nil && !d.IsDir() && !keep[rel] {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		link := ""
		if info.Mode()&fs.ModeSymlink != 0 {
			if link, err = os.Readlink(p); err != nil {
				return err
			}
		}
		hdr, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		hdr.Name = path.Join(base, rel)
		if d.IsDir() {
			hdr.Name += "/"
		}
		hdr.Uid, hdr.Gid, hdr.Uname, hdr.Gname = 0, 0, "", ""
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
	if err != nil {
		return err
	}
	return tw.Close()
}

// gitKept lists the files under the directory src that Git does not ignore, or nil to keep
// everything (gitIgnore off, src a file, or src outside a work tree).
func gitKept(src string, info fs.FileInfo, gitIgnore bool) (map[string]bool, error) {
	if !gitIgnore || !info.IsDir() {
		return nil, nil
	}
	if err := exec.Command("git", "-C", src, "rev-parse", "--is-inside-work-tree").Run(); err != nil {
		return nil, nil
	}
	out, err := exec.Command("git", "-C", src, "ls-files", "-z", "--cached", "--others", "--exclude-standard").Output()
	if err != nil {
		return nil, fmt.Errorf("listing files git keeps: %w", err)
	}
	keep := map[string]bool{}
	for _, f := range bytes.Split(out, []byte{0}) {
		if len(f) > 0 {
			keep[string(f)] = true
		}
	}
	return keep, nil
}

// readTar extracts a tar archive into dest. The archive comes from a sandbox, so it is not
// trusted: entries may not leave dest, directly or through a symlink an earlier entry made.
func readTar(r io.Reader, dest string) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	root, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
	if root, err = filepath.EvalSymlinks(root); err != nil {
		return err
	}
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := path.Clean(hdr.Name)
		if path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") {
			return fmt.Errorf("archive entry %q is outside the destination", hdr.Name)
		}
		target := filepath.Join(root, filepath.FromSlash(name))
		if err := parentInside(root, target); err != nil {
			return fmt.Errorf("archive entry %q: %w", hdr.Name, err)
		}
		mode := fs.FileMode(hdr.Mode).Perm()
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, mode|0o700); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			// Writing through a symlink an earlier entry made would leave dest.
			if fi, err := os.Lstat(target); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
				if err := os.Remove(target); err != nil {
					return err
				}
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			_ = os.Remove(target)
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		}
	}
}

// parentInside checks that target's parent directory, with symlinks resolved, is root or below
// it. Missing directories are fine: MkdirAll creates them as plain directories.
func parentInside(root, target string) error {
	dir := filepath.Dir(target)
	for {
		resolved, err := filepath.EvalSymlinks(dir)
		if err == nil {
			if resolved != root && !strings.HasPrefix(resolved, root+string(filepath.Separator)) {
				return errors.New("it would be written outside the destination")
			}
			return nil
		}
		if !errors.Is(err, fs.ErrNotExist) || dir == root {
			return err
		}
		dir = filepath.Dir(dir)
	}
}
