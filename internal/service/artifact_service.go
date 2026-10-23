package service

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
)

// ArtifactService 读取每次构建归档的产物，供构建页展示与下载回滚。
// 归档写入由 executor.ArtifactCollector 完成；本服务只读。
type ArtifactService struct {
	builds BuildStore
}

// NewArtifactService 构造产物服务。
func NewArtifactService(builds BuildStore) *ArtifactService {
	return &ArtifactService{builds: builds}
}

// Manifest 返回某次构建的产物清单；没有归档时返回空清单而不是 404，
// 这样前端在普通构建上不会报错。
func (s *ArtifactService) Manifest(ctx context.Context, pipelineID, buildID string) (*model.ArtifactManifest, error) {
	if err := validateBuildKey(pipelineID, buildID); err != nil {
		return nil, err
	}
	if _, err := s.builds.Get(ctx, pipelineID, buildID); err != nil {
		return nil, err
	}
	manifestPath := filepath.Join(s.builds.BuildDir(pipelineID, buildID), model.ArtifactManifestName)
	if !pkg.Exists(manifestPath) {
		return &model.ArtifactManifest{Files: []model.ArtifactFile{}}, nil
	}
	var manifest model.ArtifactManifest
	if err := pkg.ReadJSON(manifestPath, &manifest); err != nil {
		return nil, err
	}
	if manifest.Files == nil {
		manifest.Files = []model.ArtifactFile{}
	}
	return &manifest, nil
}

// Open 打开单个产物文件。file 必须是清单中出现过的相对路径，
// 防止通过查询参数读取 artifacts 目录以外的文件。
func (s *ArtifactService) Open(ctx context.Context, pipelineID, buildID, file string) (*os.File, *model.ArtifactFile, error) {
	manifest, err := s.Manifest(ctx, pipelineID, buildID)
	if err != nil {
		return nil, nil, err
	}
	rel, err := cleanArtifactRel(file)
	if err != nil {
		return nil, nil, err
	}
	artifact, err := findArtifact(manifest, rel)
	if err != nil {
		return nil, nil, err
	}
	target, err := s.artifactPath(pipelineID, buildID, rel)
	if err != nil {
		return nil, nil, err
	}
	if err = ensureArtifactFile(target); err != nil {
		return nil, nil, err
	}
	opened, err := os.Open(target)
	if err != nil {
		return nil, nil, fmt.Errorf("open artifact %s: %w", rel, err)
	}
	return opened, artifact, nil
}

// WriteZip 把清单中的全部产物流式写入 zip。调用方应先取 Manifest，
// 以便在写入响应头之前返回“没有产物”等错误。
func (s *ArtifactService) WriteZip(
	ctx context.Context,
	pipelineID, buildID string,
	manifest *model.ArtifactManifest,
	w io.Writer,
) error {
	if manifest == nil || len(manifest.Files) == 0 {
		return pkg.ErrNotFound("该构建没有归档产物")
	}
	// 先校验所有文件存在，尽量在开始写响应体前失败。
	for _, artifact := range manifest.Files {
		rel, relErr := cleanArtifactRel(artifact.Path)
		if relErr != nil {
			return relErr
		}
		target, pathErr := s.artifactPath(pipelineID, buildID, rel)
		if pathErr != nil {
			return pathErr
		}
		if fileErr := ensureArtifactFile(target); fileErr != nil {
			return fileErr
		}
	}

	zipWriter := zip.NewWriter(w)
	for _, artifact := range manifest.Files {
		if err := ctx.Err(); err != nil {
			_ = zipWriter.Close()
			return err
		}
		rel, _ := cleanArtifactRel(artifact.Path)
		target, _ := s.artifactPath(pipelineID, buildID, rel)
		file, err := os.Open(target)
		if err != nil {
			_ = zipWriter.Close()
			return fmt.Errorf("open artifact %s: %w", rel, err)
		}
		info, err := file.Stat()
		if err != nil {
			_ = file.Close()
			_ = zipWriter.Close()
			return fmt.Errorf("stat artifact %s: %w", rel, err)
		}
		header := &zip.FileHeader{Name: rel, Method: zip.Deflate}
		header.SetMode(info.Mode().Perm())
		header.Modified = info.ModTime()
		entry, err := zipWriter.CreateHeader(header)
		if err == nil {
			_, err = io.Copy(entry, artifactContextReader{ctx: ctx, r: file})
		}
		_ = file.Close()
		if err != nil {
			_ = zipWriter.Close()
			return fmt.Errorf("zip artifact %s: %w", rel, err)
		}
	}
	return zipWriter.Close()
}

// artifactPath 把清单中的相对路径解析为 artifacts 下的绝对路径。
func (s *ArtifactService) artifactPath(pipelineID, buildID, rel string) (string, error) {
	root := filepath.Join(s.builds.BuildDir(pipelineID, buildID), model.ArtifactDirName)
	target := filepath.Join(root, filepath.FromSlash(rel))
	if !pkg.WithinDir(root, target) {
		return "", pkg.ErrBadRequest("非法的产物路径: %s", rel)
	}
	return target, nil
}

// cleanArtifactRel 校验并归一化下载路径，拒绝绝对路径与 ../ 越界。
func cleanArtifactRel(raw string) (string, error) {
	rel := strings.TrimSpace(filepath.ToSlash(raw))
	if rel == "" {
		return "", pkg.ErrBadRequest("缺少产物文件参数")
	}
	rel = path.Clean(strings.TrimPrefix(rel, "./"))
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") ||
		strings.HasPrefix(rel, "/") || filepath.IsAbs(filepath.FromSlash(rel)) {
		return "", pkg.ErrBadRequest("非法的产物路径: %s", raw)
	}
	return rel, nil
}

// findArtifact 只在清单中查找，避免用户请求任意本地文件。
func findArtifact(manifest *model.ArtifactManifest, rel string) (*model.ArtifactFile, error) {
	for i := range manifest.Files {
		if manifest.Files[i].Path == rel {
			return &manifest.Files[i], nil
		}
	}
	return nil, pkg.ErrNotFound("产物不存在: %s", rel)
}

// ensureArtifactFile 只允许普通文件；归档时已跳过符号链接，
// 这里再挡一次手工放入 artifacts 目录的链接。
func ensureArtifactFile(target string) error {
	info, err := os.Lstat(target)
	if err != nil {
		if os.IsNotExist(err) {
			return pkg.ErrNotFound("产物文件已丢失: %s", filepath.Base(target))
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return pkg.ErrBadRequest("不允许下载符号链接产物")
	}
	if !info.Mode().IsRegular() {
		return pkg.ErrNotFound("产物不是普通文件: %s", filepath.Base(target))
	}
	return nil
}

// validateBuildKey 限制 pid/bid 只能是单层目录名，防止 ../ 读到 jobs 外。
func validateBuildKey(pipelineID, buildID string) error {
	for _, value := range []string{pipelineID, buildID} {
		if value == "" || value == "." || value == ".." ||
			strings.Contains(value, "/") || strings.Contains(value, string(filepath.Separator)) ||
			strings.Contains(value, ":") {
			return pkg.ErrBadRequest("非法的构建路径")
		}
	}
	return nil
}

// artifactContextReader 让 zip 流式复制也能响应取消/超时。
type artifactContextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r artifactContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
