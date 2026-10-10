package app

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// Native configuration/state directories are not capability packages. Keep
// external registration available, but don't turn account state into a managed
// resource by recursively copying a CLI home.
func validateManagedAssetSource(path string) error {
	actual, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("能力来源无法读取")
	}
	return filepath.WalkDir(actual, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("能力来源无法读取：%s", p)
		}
		name := strings.ToLower(d.Name())
		blocked := name == ".git" || name == ".ssh" || name == "sessions" || name == "projects" || name == "auth.json" || name == ".credentials.json" || name == ".claude.json" || name == "settings.json" || name == "settings.local.json" || name == "config.toml" || name == ".npmrc" || name == ".netrc" || strings.HasPrefix(name, ".env") || strings.Contains(name, ".sqlite") || strings.HasSuffix(name, ".pem") || strings.HasSuffix(name, ".key")
		if blocked {
			return fmt.Errorf("不能把原生配置、账号或会话状态导入能力版本：%s；请登记外部来源或选择独立能力目录", p)
		}
		if d.Type()&os.ModeSymlink != 0 || !d.IsDir() && !d.Type().IsRegular() {
			return fmt.Errorf("能力资源包含符号链接或特殊文件：%s", p)
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		if info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
			return fmt.Errorf("能力资源含特殊权限：%s", p)
		}
		return nil
	})
}

func (a *Application) copyManagedCapability(asset CapabilityAsset, expected SourceStamp) (string, SourceStamp, error) {
	a.mu.Lock()
	stateDir := a.config.StateDir
	a.mu.Unlock()
	directory := filepath.Join(stateDir, "capabilities", "versions", uuid.NewString())
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", SourceStamp{}, err
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(directory)
		}
	}()
	actual, err := filepath.EvalSymlinks(asset.Source)
	if err != nil {
		return "", SourceStamp{}, err
	}
	if err = validateManagedAssetSource(asset.Source); err != nil {
		return "", SourceStamp{}, err
	}
	info, err := os.Stat(actual)
	if err != nil {
		return "", SourceStamp{}, err
	}
	rootPath := actual
	if !info.IsDir() {
		rootPath = filepath.Dir(actual)
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return "", SourceStamp{}, err
	}
	defer root.Close()
	destination := filepath.Join(directory, filepath.Base(actual))
	var total int64
	count := 0
	dirs := []struct {
		path string
		mode fs.FileMode
	}{}
	err = filepath.WalkDir(actual, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		count++
		if count > 2048 {
			return fmt.Errorf("来源条目超过 2048")
		}
		metadata, e := d.Info()
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(actual, p)
		if e != nil {
			return e
		}
		target := filepath.Join(destination, rel)
		if d.IsDir() {
			if e = os.Mkdir(target, 0700); e != nil {
				return e
			}
			dirs = append(dirs, struct {
				path string
				mode fs.FileMode
			}{target, metadata.Mode().Perm()})
			return nil
		}
		if !metadata.Mode().IsRegular() {
			return fmt.Errorf("资源复制期间出现特殊文件")
		}
		from, e := filepath.Rel(rootPath, p)
		if e != nil {
			return e
		}
		input, e := root.Open(from)
		if e != nil {
			return e
		}
		defer input.Close()
		opened, e := input.Stat()
		if e != nil || !os.SameFile(metadata, opened) {
			return fmt.Errorf("复制期间资源身份已变化")
		}
		output, e := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		n, e := io.Copy(output, io.LimitReader(input, (32<<20)-total+1))
		total += n
		if e == nil && total > 32<<20 {
			e = fmt.Errorf("来源总大小超过 32 MiB")
		}
		if e == nil {
			e = output.Chmod(metadata.Mode().Perm())
		}
		if e == nil {
			e = output.Sync()
		}
		closeErr := output.Close()
		if e == nil {
			e = closeErr
		}
		return e
	})
	if err != nil {
		return "", SourceStamp{}, err
	}
	for n := len(dirs) - 1; n >= 0; n-- {
		if err = os.Chmod(dirs[n].path, dirs[n].mode); err != nil {
			return "", SourceStamp{}, err
		}
		if err = syncCapabilityDir(dirs[n].path); err != nil {
			return "", SourceStamp{}, err
		}
	}
	current, err := stampSource(asset.Source, false)
	if err != nil || current != expected {
		return "", SourceStamp{}, fmt.Errorf("导入期间来源已变化，请重新预览")
	}
	copied, err := stampSource(destination, false)
	if err != nil {
		return "", SourceStamp{}, err
	}
	if copied.Kind != expected.Kind || copied.SHA256 != expected.SHA256 {
		return "", SourceStamp{}, fmt.Errorf("固定版本副本与预览内容不一致")
	}
	for _, dir := range []string{directory, filepath.Dir(directory), filepath.Dir(filepath.Dir(directory)), stateDir} {
		if err = syncCapabilityDir(dir); err != nil {
			return "", SourceStamp{}, err
		}
	}
	complete = true
	return destination, copied, nil
}
func syncCapabilityDir(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}
