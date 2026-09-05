package registry

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	githubAPI = "https://api.github.com"
	owner     = "Githy912"
	repo      = "wpkm-pkgs"
)

type Manifest struct {
	Name         string `json:"name"`
	Version      string `json:"version"`
	Description  string `json:"description"`
	Architecture string `json:"architecture"`
	Binary       string `json:"binary"`
	SHA3_512     string `json:"sha3-512"`
}

type GitHubClient struct {
	Token      string
	HTTPClient *http.Client
}

type githubReference struct {
	Object struct {
		SHA string `json:"sha"`
	} `json:"object"`
}

type githubRepository struct {
	DefaultBranch string `json:"default_branch"`
}

type githubBlob struct {
	SHA string `json:"sha"`
}

type githubTree struct {
	SHA string `json:"sha"`
}

type githubCommit struct {
	SHA string `json:"sha"`
}

type githubPullRequest struct {
	HTMLURL string `json:"html_url"`
}

func NewClient() (*GitHubClient, error) {
	token := strings.TrimSpace(
		os.Getenv("WPKM_GITHUB_TOKEN"),
	)

	if token == "" {
		return nil, errors.New(
			"WPKM_GITHUB_TOKEN is not set",
		)
	}

	return &GitHubClient{
		Token: token,
		HTTPClient: &http.Client{
			Timeout: 2 * time.Minute,
		},
	}, nil
}

func (c *GitHubClient) request(
	method string,
	url string,
	body any,
	result any,
) error {
	var reader io.Reader

	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf(
				"encode request: %w",
				err,
			)
		}

		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(
		method,
		url,
		reader,
	)
	if err != nil {
		return fmt.Errorf(
			"create request: %w",
			err,
		)
	}

	req.Header.Set(
		"Accept",
		"application/vnd.github+json",
	)

	req.Header.Set(
		"Authorization",
		"Bearer "+c.Token,
	)

	req.Header.Set(
		"X-GitHub-Api-Version",
		"2026-03-10",
	)

	req.Header.Set(
		"User-Agent",
		"wpkm",
	)

	if body != nil {
		req.Header.Set(
			"Content-Type",
			"application/json",
		)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf(
			"GitHub request failed: %w",
			err,
		)
	}

	defer resp.Body.Close()

	responseData, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf(
			"read GitHub response: %w",
			err,
		)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf(
			"GitHub API returned HTTP %d: %s",
			resp.StatusCode,
			strings.TrimSpace(
				string(responseData),
			),
		)
	}

	if result != nil && len(responseData) > 0 {
		if err := json.Unmarshal(
			responseData,
			result,
		); err != nil {
			return fmt.Errorf(
				"decode GitHub response: %w",
				err,
			)
		}
	}

	return nil
}

func (c *GitHubClient) getDefaultBranch() (string, error) {
	var repository githubRepository

	err := c.request(
		http.MethodGet,
		fmt.Sprintf(
			"%s/repos/%s/%s",
			githubAPI,
			owner,
			repo,
		),
		nil,
		&repository,
	)

	if err != nil {
		return "", fmt.Errorf(
			"get repository: %w",
			err,
		)
	}

	if strings.TrimSpace(
		repository.DefaultBranch,
	) == "" {
		return "", errors.New(
			"GitHub repository has no default branch",
		)
	}

	return repository.DefaultBranch, nil
}

func (c *GitHubClient) getBranchSHA(
	branch string,
) (string, error) {
	var reference githubReference

	err := c.request(
		http.MethodGet,
		fmt.Sprintf(
			"%s/repos/%s/%s/git/ref/heads/%s",
			githubAPI,
			owner,
			repo,
			branch,
		),
		nil,
		&reference,
	)

	if err != nil {
		return "", fmt.Errorf(
			"get branch: %w",
			err,
		)
	}

	if strings.TrimSpace(
		reference.Object.SHA,
	) == "" {
		return "", errors.New(
			"GitHub branch returned no commit SHA",
		)
	}

	return reference.Object.SHA, nil
}

func (c *GitHubClient) createBlob(
	content []byte,
	encoding string,
) (string, error) {
	var blobContent string

	switch encoding {
	case "utf-8":
		blobContent = string(content)

	case "base64":
		blobContent = string(content)

	default:
		return "", fmt.Errorf(
			"unsupported blob encoding: %s",
			encoding,
		)
	}

	payload := map[string]any{
		"content":  blobContent,
		"encoding": encoding,
	}

	var blob githubBlob

	err := c.request(
		http.MethodPost,
		fmt.Sprintf(
			"%s/repos/%s/%s/git/blobs",
			githubAPI,
			owner,
			repo,
		),
		payload,
		&blob,
	)

	if err != nil {
		return "", fmt.Errorf(
			"create blob: %w",
			err,
		)
	}

	if strings.TrimSpace(
		blob.SHA,
	) == "" {
		return "", errors.New(
			"GitHub blob returned no SHA",
		)
	}

	return blob.SHA, nil
}

func (c *GitHubClient) createTree(
	baseTree string,
	entries []map[string]any,
) (string, error) {
	payload := map[string]any{
		"base_tree": baseTree,
		"tree":      entries,
	}

	var tree githubTree

	err := c.request(
		http.MethodPost,
		fmt.Sprintf(
			"%s/repos/%s/%s/git/trees",
			githubAPI,
			owner,
			repo,
		),
		payload,
		&tree,
	)

	if err != nil {
		return "", fmt.Errorf(
			"create tree: %w",
			err,
		)
	}

	if strings.TrimSpace(
		tree.SHA,
	) == "" {
		return "", errors.New(
			"GitHub tree returned no SHA",
		)
	}

	return tree.SHA, nil
}

func (c *GitHubClient) createCommit(
	message string,
	treeSHA string,
	parentSHA string,
) (string, error) {
	payload := map[string]any{
		"message": message,
		"tree":    treeSHA,
		"parents": []string{
			parentSHA,
		},
	}

	var commit githubCommit

	err := c.request(
		http.MethodPost,
		fmt.Sprintf(
			"%s/repos/%s/%s/git/commits",
			githubAPI,
			owner,
			repo,
		),
		payload,
		&commit,
	)

	if err != nil {
		return "", fmt.Errorf(
			"create commit: %w",
			err,
		)
	}

	if strings.TrimSpace(
		commit.SHA,
	) == "" {
		return "", errors.New(
			"GitHub commit returned no SHA",
		)
	}

	return commit.SHA, nil
}

func (c *GitHubClient) createBranch(
	branch string,
	commitSHA string,
) error {
	payload := map[string]any{
		"ref": "refs/heads/" + branch,
		"sha": commitSHA,
	}

	err := c.request(
		http.MethodPost,
		fmt.Sprintf(
			"%s/repos/%s/%s/git/refs",
			githubAPI,
			owner,
			repo,
		),
		payload,
		nil,
	)

	if err != nil {
		return fmt.Errorf(
			"create Git branch: %w",
			err,
		)
	}

	return nil
}

func (c *GitHubClient) createPullRequest(
	title string,
	head string,
	base string,
	body string,
) (string, error) {
	payload := map[string]any{
		"title": title,
		"head":  head,
		"base":  base,
		"body":  body,
	}

	var pullRequest githubPullRequest

	err := c.request(
		http.MethodPost,
		fmt.Sprintf(
			"%s/repos/%s/%s/pulls",
			githubAPI,
			owner,
			repo,
		),
		payload,
		&pullRequest,
	)

	if err != nil {
		return "", fmt.Errorf(
			"create pull request: %w",
			err,
		)
	}

	if strings.TrimSpace(
		pullRequest.HTMLURL,
	) == "" {
		return "", errors.New(
			"GitHub pull request returned no URL",
		)
	}

	return pullRequest.HTMLURL, nil
}

// sanitizeRefComponent converts a package name or version
// into a safe Git reference component.
//
// Spaces become '-'. Unsupported characters also become '-'.
func sanitizeRefComponent(value string) string {
	value = strings.TrimSpace(value)

	var builder strings.Builder

	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)

		case r >= 'A' && r <= 'Z':
			builder.WriteRune(r)

		case r >= '0' && r <= '9':
			builder.WriteRune(r)

		case r == '-' || r == '_' || r == '.':
			builder.WriteRune(r)

		case r == ' ':
			builder.WriteRune('-')

		default:
			builder.WriteRune('-')
		}
	}

	result := builder.String()

	result = strings.Trim(
		result,
		"-.",
	)

	if result == "" {
		return "package"
	}

	return result
}

func (c *GitHubClient) CreatePackagePullRequest(
	packageName string,
	manifest Manifest,
	filePath string,
) (string, error) {
	defaultBranch, err := c.getDefaultBranch()
	if err != nil {
		return "", err
	}

	parentSHA, err := c.getBranchSHA(
		defaultBranch,
	)
	if err != nil {
		return "", err
	}

	fmt.Println("Uploading manifest...")

	manifestData, err := json.MarshalIndent(
		manifest,
		"",
		"  ",
	)
	if err != nil {
		return "", fmt.Errorf(
			"encode manifest: %w",
			err,
		)
	}

	manifestData = append(
		manifestData,
		'\n',
	)

	manifestBlob, err := c.createBlob(
		manifestData,
		"utf-8",
	)
	if err != nil {
		return "", fmt.Errorf(
			"upload manifest: %w",
			err,
		)
	}

	fmt.Println("Uploading package binary...")

	binaryData, err := os.ReadFile(
		filePath,
	)
	if err != nil {
		return "", fmt.Errorf(
			"read package binary: %w",
			err,
		)
	}

	binaryBase64 := base64.StdEncoding.EncodeToString(
		binaryData,
	)

	binaryBlob, err := c.createBlob(
		[]byte(binaryBase64),
		"base64",
	)
	if err != nil {
		return "", fmt.Errorf(
			"upload package binary: %w",
			err,
		)
	}

	fmt.Println("Uploading SHA3-512...")

	hashData := []byte(
		strings.TrimSpace(
			manifest.SHA3_512,
		) + "\n",
	)

	hashBlob, err := c.createBlob(
		hashData,
		"utf-8",
	)
	if err != nil {
		return "", fmt.Errorf(
			"upload SHA3-512: %w",
			err,
		)
	}

	packagePath := "packages/" + packageName

	treeEntries := []map[string]any{
		{
			"path": packagePath + "/manifest.json",
			"mode": "100644",
			"type": "blob",
			"sha":  manifestBlob,
		},
		{
			"path": packagePath + "/" + filepath.Base(filePath),
			"mode": "100644",
			"type": "blob",
			"sha":  binaryBlob,
		},
		{
			"path": packagePath + "/SHA3-512",
			"mode": "100644",
			"type": "blob",
			"sha":  hashBlob,
		},
	}

	fmt.Println("Creating Git tree...")

	treeSHA, err := c.createTree(
		parentSHA,
		treeEntries,
	)
	if err != nil {
		return "", err
	}

	fmt.Println("Creating commit...")

	commitSHA, err := c.createCommit(
		fmt.Sprintf(
			"Add %s %s",
			packageName,
			manifest.Version,
		),
		treeSHA,
		parentSHA,
	)
	if err != nil {
		return "", err
	}

	// Git branch names cannot contain spaces.
	// Keep the actual package name untouched and
	// sanitize only the branch reference.
	safePackageName := sanitizeRefComponent(
		packageName,
	)

	safeVersion := sanitizeRefComponent(
		manifest.Version,
	)

	branch := fmt.Sprintf(
		"wpkm/%s/%s",
		safePackageName,
		safeVersion,
	)

	fmt.Printf(
		"Creating branch %s...\n",
		branch,
	)

	if err := c.createBranch(
		branch,
		commitSHA,
	); err != nil {
		return "", err
	}

	fmt.Println("Creating pull request...")

	prURL, err := c.createPullRequest(
		fmt.Sprintf(
			"Add %s %s",
			packageName,
			manifest.Version,
		),
		branch,
		defaultBranch,
		fmt.Sprintf(
			"Automated WPKM package submission.\n\n"+
				"Package: %s\n"+
				"Version: %s\n"+
				"SHA3-512: %s\n\n"+
				"The WPKM verification bot will independently "+
				"calculate the SHA3-512 hash.",
			packageName,
			manifest.Version,
			manifest.SHA3_512,
		),
	)

	if err != nil {
		return "", err
	}

	return prURL, nil
}
