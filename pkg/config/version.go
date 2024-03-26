package config

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/cheggaaa/pb/v3"
	"golang.org/x/net/context/ctxhttp"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Release collects data about a single release on GitHub.
type Release struct {
	Name        string    `json:"name"`
	TagName     string    `json:"tag_name"`
	Draft       bool      `json:"draft"`
	PreRelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []Asset   `json:"assets"`

	Version string `json:"-"` // set manually in the code
}

// Asset is a file uploaded and attached to a release.
type Asset struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

const githubAPITimeout = 30 * time.Second

// githubError is returned by the GitHub API, e.g. for rate-limiting.
type githubError struct {
	Message string
}

// GitHubLatestRelease uses the GitHub API to get information about the specific
// release of a repository.
func GitHubLatestRelease(ctx context.Context, ver string, owner, repo string) (Release, error) {
	ctx, cancel := context.WithTimeout(ctx, githubAPITimeout)
	defer cancel()

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases", owner, repo)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return Release{}, err
	}

	// pin API version 3
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	res, err := ctxhttp.Do(ctx, http.DefaultClient, req)
	if err != nil {
		return Release{}, err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		content := res.Header.Get("Content-Type")
		if strings.Contains(content, "application/json") {
			// try to decode error message
			var msg githubError
			jerr := json.NewDecoder(res.Body).Decode(&msg)
			if jerr == nil {
				return Release{}, fmt.Errorf("unexpected status %v (%v) returned, message:\n  %v", res.StatusCode, res.Status, msg.Message)
			}
		}

		return Release{}, fmt.Errorf("unexpected status %v (%v) returned", res.StatusCode, res.Status)
	}

	buf, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return Release{}, err
	}

	var release Release
	var releaseList []Release
	err = json.Unmarshal(buf, &releaseList)
	if err != nil {
		return Release{}, err
	}
	if ver == "0" {
		release = releaseList[0]
		log.Printf("latest version is %v / %v", release.TagName, release.PublishedAt.Local())
	} else {
		for _, r := range releaseList {
			if r.TagName == ver {
				release = r
				break
			}
		}
	}

	if release.TagName == "" {
		return Release{}, fmt.Errorf("can not find the specific version")
	}

	release.Version = release.TagName
	return release, nil
}

func DownloadRelease(ctx context.Context, os, arch string, isLargeDisk, isFull bool, destination string, ver string) (version string, err error) {
	rel, err := GitHubLatestRelease(ctx, ver, "seaweedfs", "seaweedfs")
	if err != nil {
		return "", err
	}

	log.Printf("download version: %s", rel.Version)

	largeDiskSuffix := ""
	if isLargeDisk {
		largeDiskSuffix = "_large_disk"
	}

	fullSuffix := ""
	if isFull {
		fullSuffix = "_full"
	}

	ext := "tar.gz"

	suffix := fmt.Sprintf("%s_%s%s%s.%s", os, arch, fullSuffix, largeDiskSuffix, ext)
	md5Filename := fmt.Sprintf("%s.md5", suffix)
	_, md5Val, err := getGithubDataFile(ctx, rel.Assets, md5Filename)
	if err != nil {
		return "", err
	}

	downloadFilename, buf, err := getGithubDataFile(ctx, rel.Assets, suffix)
	if err != nil {
		return "", err
	}

	md5Ctx := md5.New()
	md5Ctx.Write(buf)
	binaryMd5 := md5Ctx.Sum(nil)
	if hex.EncodeToString(binaryMd5) != string(md5Val[0:32]) {
		log.Printf("md5:'%s' '%s'", hex.EncodeToString(binaryMd5), string(md5Val[0:32]))
		err = fmt.Errorf("binary md5sum doesn't match")
		return "", err
	}

	err = extractToFile(buf, downloadFilename, destination)
	if err != nil {
		return "", err
	} else {
		log.Printf("successfully updated weed to version %v", rel.Version)
	}

	return rel.Version, nil
}

func getGithubData(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	// request binary data
	req.Header.Set("Accept", "application/octet-stream")

	res, err := ctxhttp.Do(ctx, http.DefaultClient, req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %v (%v) returned", res.StatusCode, res.Status)
	}

	readerCloser := withProgressBar(res.Body, int(res.ContentLength))
	defer readerCloser.Close()

	buf, err := ioutil.ReadAll(readerCloser)
	if err != nil {
		return nil, err
	}

	return buf, nil
}

func getGithubDataFile(ctx context.Context, assets []Asset, suffix string) (filename string, data []byte, err error) {
	var url string
	for _, a := range assets {
		if strings.HasSuffix(a.Name, suffix) {
			url = a.URL
			filename = a.Name
			break
		}
	}

	if url == "" {
		return "", nil, fmt.Errorf("unable to find file with suffix %v", suffix)
	}

	log.Printf("download %v\n", filename)
	data, err = getGithubData(ctx, url)
	if err != nil {
		return "", nil, err
	}

	return filename, data, nil
}

func extractToFile(buf []byte, filename, target string) error {
	var rd io.Reader = bytes.NewReader(buf)

	switch filepath.Ext(filename) {
	case ".gz":
		gr, err := gzip.NewReader(rd)
		if err != nil {
			return err
		}
		defer gr.Close()
		trd := tar.NewReader(gr)
		hdr, terr := trd.Next()
		if terr != nil {
			log.Printf("uncompress file(%s) failed:%s", hdr.Name, terr)
			return terr
		}
		rd = trd
	case ".zip":
		zrd, err := zip.NewReader(bytes.NewReader(buf), int64(len(buf)))
		if err != nil {
			return err
		}

		if len(zrd.File) != 1 {
			return fmt.Errorf("ZIP archive contains more than one file")
		}

		file, err := zrd.File[0].Open()
		if err != nil {
			return err
		}

		defer func() {
			_ = file.Close()
		}()

		rd = file
	}

	// Write everything to a temp file
	dir := filepath.Dir(target)
	new, err := ioutil.TempFile(dir, "weed")
	if err != nil {
		return err
	}

	n, err := io.Copy(new, rd)
	if err != nil {
		_ = new.Close()
		_ = os.Remove(new.Name())
		return err
	}
	if err = new.Sync(); err != nil {
		return err
	}
	if err = new.Close(); err != nil {
		return err
	}

	mode := os.FileMode(0755)
	// attempt to find the original mode
	if fi, err := os.Lstat(target); err == nil {
		mode = fi.Mode()
	}

	// Rename the temp file to the final location atomically.
	if err := os.Rename(new.Name(), target); err != nil {
		return err
	}

	log.Printf("saved %d bytes in %v\n", n, target)
	return os.Chmod(target, mode)
}

func withProgressBar(r io.ReadCloser, length int) io.ReadCloser {
	bar := pb.Simple.New(length).Start()
	return bar.NewProxyReader(r)
}
