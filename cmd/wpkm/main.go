package main

import (
	"archive/zip"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/Githy912/wpkm/internal/auth"
	"github.com/Githy912/wpkm/internal/hash"
	"github.com/Githy912/wpkm/internal/registry"
)

const (
	version = "0.1.0"

	registryRawBase = "https://raw.githubusercontent.com/Githy912/wpkm-pkgs/main/packages"
	githubAPIBase   = "https://api.github.com/repos/Githy912/wpkm-pkgs"

	colorReset   = "\033[0m"
	colorRed     = "\033[31m"
	colorGreen   = "\033[32m"
	colorYellow  = "\033[33m"
	colorBlue    = "\033[34m"
	colorMagenta = "\033[35m"
	colorCyan    = "\033[36m"
	colorBold    = "\033[1m"

	ansi256 = "\033[38;5;%dm"

	progressWidth = 28
)

var (
	httpClient = &http.Client{}

	consoleMutex sync.Mutex
)

type githubContent struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Type string `json:"type"`
}

// ------------------------------------------------------------
// MAIN
// ------------------------------------------------------------

func main() {
	enableVirtualTerminal()

	if len(os.Args) < 2 {
		printHelp()
		return
	}

	command := strings.ToLower(
		strings.TrimSpace(os.Args[1]),
	)

	switch command {
	case "push":
		handlePush(os.Args[2:])

	case "get":
		handleGet(os.Args[2:])

	case "purge":
		handlePurge(os.Args[2:])

	case "ask":
		handleAsk(os.Args[2:])

	case "where":
		handleWhere(os.Args[2:])

	case "list":
		handleList(os.Args[2:])

	case "about":
		handleAbout(os.Args[2:])

	case "version", "--version", "-v":
		handleVersion(os.Args[2:])

	case "help", "-h", "--help":
		printHelp()

	default:
		errorf("unknown command '%s'", command)
		fmt.Println()
		printHelp()
		os.Exit(1)
	}
}

// ------------------------------------------------------------
// WINDOWS TERMINAL
// ------------------------------------------------------------

func enableVirtualTerminal() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")

	getConsoleMode := kernel32.NewProc("GetConsoleMode")
	setConsoleMode := kernel32.NewProc("SetConsoleMode")
	getStdHandle := kernel32.NewProc("GetStdHandle")

	const stdoutHandle = ^uintptr(10)
	const enableVirtualTerminalProcessing = 0x0004

	handle, _, _ := getStdHandle.Call(stdoutHandle)

	if handle == 0 || handle == ^uintptr(0) {
		return
	}

	var mode uint32

	ret, _, _ := getConsoleMode.Call(
		handle,
		uintptr(unsafe.Pointer(&mode)),
	)

	if ret == 0 {
		return
	}

	if mode&enableVirtualTerminalProcessing != 0 {
		return
	}

	_, _, _ = setConsoleMode.Call(
		handle,
		uintptr(mode|enableVirtualTerminalProcessing),
	)
}

// ------------------------------------------------------------
// OUTPUT HELPERS
// ------------------------------------------------------------

func errorf(format string, args ...any) {
	fmt.Printf(
		"%sError:%s %s\n",
		colorRed+colorBold,
		colorReset,
		fmt.Sprintf(format, args...),
	)
}

func warningf(format string, args ...any) {
	fmt.Printf(
		"%sWarning:%s %s\n",
		colorYellow+colorBold,
		colorReset,
		fmt.Sprintf(format, args...),
	)
}

func successf(format string, args ...any) {
	fmt.Printf(
		"%s%sSuccess:%s %s\n",
		colorGreen,
		colorBold,
		colorReset,
		fmt.Sprintf(format, args...),
	)
}

func infof(format string, args ...any) {
	fmt.Printf(
		"%sInfo:%s %s\n",
		colorCyan+colorBold,
		colorReset,
		fmt.Sprintf(format, args...),
	)
}

// ------------------------------------------------------------
// RAINBOW PROGRESS
// ------------------------------------------------------------

var rainbowColors = []int{
	196,
	202,
	226,
	46,
	51,
	21,
	93,
	201,
}

func rainbowColor(offset int, position int) int {
	index := (offset + position) % len(rainbowColors)
	return rainbowColors[index]
}

func renderProgress(
	label string,
	fraction float64,
	offset int,
) {
	if fraction < 0 {
		fraction = 0
	}

	if fraction > 1 {
		fraction = 1
	}

	filled := int(
		float64(progressWidth) * fraction,
	)

	consoleMutex.Lock()
	defer consoleMutex.Unlock()

	fmt.Print("\r\033[2K")

	fmt.Printf(
		"%s%s%s ",
		colorBold,
		label,
		colorReset,
	)

	for i := 0; i < progressWidth; i++ {
		if i < filled {
			fmt.Printf(
				ansi256,
				rainbowColor(offset, i),
			)

			fmt.Print("█")
		} else {
			fmt.Print("░")
		}
	}

	fmt.Printf(
		" %3d%%",
		int(fraction*100),
	)
}

func finishProgress(label string) {
	consoleMutex.Lock()
	defer consoleMutex.Unlock()

	fmt.Print("\r\033[2K")

	fmt.Printf(
		"%s%s%s\n",
		colorGreen+colorBold,
		label,
		colorReset,
	)
}

func animatedProgress(
	label string,
	done <-chan struct{},
) {
	ticker := time.NewTicker(
		60 * time.Millisecond,
	)

	defer ticker.Stop()

	offset := 0
	position := 0

	for {
		select {
		case <-done:
			finishProgress(label)
			return

		case <-ticker.C:
			renderProgress(
				label,
				float64((position%100)+1)/100.0,
				offset,
			)

			offset++
			position += 3
		}
	}
}

func startIndeterminateProgress(
	label string,
) func() {
	done := make(chan struct{})

	go animatedProgress(
		label,
		done,
	)

	return func() {
		close(done)
	}
}

// ------------------------------------------------------------
// PUSH
// ------------------------------------------------------------

func handlePush(args []string) {
	if len(args) < 1 {
		errorf("Usage: wpkm push <file>")
		return
	}

	filePath := args[0]

	info, err := os.Stat(filePath)

	if err != nil {
		errorf(
			"cannot access '%s': %v",
			filePath,
			err,
		)
		return
	}

	if info.IsDir() {
		errorf(
			"'%s' is a directory",
			filePath,
		)
		return
	}

	fmt.Println()

	fmt.Printf(
		"%sPushing package:%s %s\n",
		colorBold+colorMagenta,
		colorReset,
		filepath.Base(filePath),
	)

	fmt.Printf(
		"Size: %d bytes\n",
		info.Size(),
	)

	fmt.Println()

	stopProgress := startIndeterminateProgress(
		"Calculating SHA3-512...",
	)

	fileHash, err := hash.File(filePath)

	stopProgress()

	if err != nil {
		errorf("%v", err)
		return
	}

	fmt.Printf(
		"SHA3-512: %s%s%s\n",
		colorBlue,
		fileHash,
		colorReset,
	)

	fmt.Println()

	packageName := strings.TrimSuffix(
		filepath.Base(filePath),
		filepath.Ext(filePath),
	)

	binaryName := filepath.Base(filePath)

	manifest := registry.Manifest{
		Name:         packageName,
		Version:      version,
		Description:  "WPKM package",
		Architecture: "amd64",
		Binary:       binaryName,
		SHA3_512:     fileHash,
	}

	infof(
		"Checking GitHub authentication...",
	)

	token, err := auth.GetOrPrompt()

	if err != nil {
		errorf("%v", err)
		return
	}

	if err := os.Setenv(
		"WPKM_GITHUB_TOKEN",
		token,
	); err != nil {
		errorf(
			"configure GitHub authentication: %v",
			err,
		)
		return
	}

	successf(
		"GitHub authentication ready.",
	)

	fmt.Println()

	infof(
		"Creating package pull request...",
	)

	client, err := registry.NewClient()

	if err != nil {
		errorf(
			"create GitHub client: %v",
			err,
		)
		return
	}

	prURL, err := client.CreatePackagePullRequest(
		packageName,
		manifest,
		filePath,
	)

	if err != nil {
		errorf(
			"publish package: %v",
			err,
		)
		return
	}

	fmt.Println()

	fmt.Println(
		colorBold +
			colorMagenta +
			"========================================" +
			colorReset,
	)

	fmt.Println(
		colorBold +
			colorMagenta +
			" PACKAGE SUBMITTED" +
			colorReset,
	)

	fmt.Println(
		colorBold +
			colorMagenta +
			"========================================" +
			colorReset,
	)

	fmt.Printf(
		"Package: %s%s%s\n",
		colorMagenta,
		packageName,
		colorReset,
	)

	fmt.Printf(
		"Version: %s%s%s\n",
		colorBlue,
		version,
		colorReset,
	)

	fmt.Printf(
		"SHA3-512: %s%s%s\n",
		colorBlue,
		fileHash,
		colorReset,
	)

	fmt.Printf(
		"Pull request: %s%s%s\n",
		colorCyan,
		prURL,
		colorReset,
	)

	fmt.Println()

	fmt.Println(
		"The WPKM verification bot will independently",
	)

	fmt.Println(
		"calculate the SHA3-512 hash.",
	)

	fmt.Println()

	fmt.Println("If the hashes match:")
	fmt.Println(
		"  MATCH SHA3-512 > AUTO-MERGE > LIVE",
	)

	fmt.Println()

	fmt.Println("If they mismatch:")
	fmt.Println(
		"  MISMATCH > FAIL PR",
	)

	fmt.Println(
		colorBold +
			colorMagenta +
			"========================================" +
			colorReset,
	)
}

// ------------------------------------------------------------
// GET
// ------------------------------------------------------------

func handleGet(args []string) {
	if len(args) < 1 {
		errorf("Usage: wpkm get <package>")
		return
	}

	packageName := strings.TrimSpace(
		strings.Join(args, " "),
	)

	if packageName == "" {
		errorf("Usage: wpkm get <package>")
		return
	}

	fmt.Println()

	fmt.Printf(
		"%sGetting package:%s '%s'\n",
		colorBold+colorMagenta,
		colorReset,
		packageName,
	)

	fmt.Println()

	infof(
		"Fetching package manifest...",
	)

	manifestURL := registryPackageURL(
		packageName,
		"manifest.json",
	)

	stopProgress := startIndeterminateProgress(
		"Fetching manifest...",
	)

	manifestData, err := downloadURL(
		manifestURL,
	)

	stopProgress()

	if err != nil {
		errorf(
			"package not found: %v",
			err,
		)
		return
	}

	var manifest registry.Manifest

	if err := json.Unmarshal(
		manifestData,
		&manifest,
	); err != nil {
		errorf(
			"invalid package manifest: %v",
			err,
		)
		return
	}

	if err := validateManifest(
		packageName,
		manifest,
	); err != nil {
		errorf(
			"invalid package manifest: %v",
			err,
		)
		return
	}

	expectedHash := strings.ToLower(
		strings.TrimSpace(
			manifest.SHA3_512,
		),
	)

	fmt.Printf(
		"Package: %s%s%s\n",
		colorMagenta,
		manifest.Name,
		colorReset,
	)

	fmt.Printf(
		"Version: %s%s%s\n",
		colorBlue,
		manifest.Version,
		colorReset,
	)

	fmt.Printf(
		"Architecture: %s%s%s\n",
		colorBlue,
		manifest.Architecture,
		colorReset,
	)

	fmt.Printf(
		"Binary: %s%s%s\n",
		colorCyan,
		manifest.Binary,
		colorReset,
	)

	fmt.Println()

	binaryURL := registryPackageURL(
		packageName,
		manifest.Binary,
	)

	tempDir, err := os.MkdirTemp(
		"",
		"wpkm-get-*",
	)

	if err != nil {
		errorf(
			"create temporary directory: %v",
			err,
		)
		return
	}

	defer os.RemoveAll(tempDir)

	tempBinary := filepath.Join(
		tempDir,
		manifest.Binary,
	)

	if err := os.MkdirAll(
		filepath.Dir(tempBinary),
		0755,
	); err != nil {
		errorf(
			"create temporary package directory: %v",
			err,
		)
		return
	}

	if err := downloadToFileWithProgress(
		binaryURL,
		tempBinary,
	); err != nil {
		errorf(
			"download package: %v",
			err,
		)
		return
	}

	stopProgress = startIndeterminateProgress(
		"Verifying SHA3-512...",
	)

	actualHash, err := hash.File(
		tempBinary,
	)

	stopProgress()

	if err != nil {
		errorf(
			"calculate package hash: %v",
			err,
		)
		return
	}

	actualHash = strings.ToLower(
		strings.TrimSpace(actualHash),
	)

	fmt.Printf(
		"Expected: %s%s%s\n",
		colorBlue,
		expectedHash,
		colorReset,
	)

	fmt.Printf(
		"Actual:   %s%s%s\n",
		colorBlue,
		actualHash,
		colorReset,
	)

	fmt.Println()

	if actualHash != expectedHash {
		errorf(
			"SHA3-512 mismatch",
		)

		warningf(
			"The downloaded package failed integrity verification.",
		)

		infof(
			"Deleting temporary package...",
		)

		if err := os.Remove(
			tempBinary,
		); err != nil &&
			!os.IsNotExist(err) {
			warningf(
				"could not delete temporary package: %v",
				err,
			)
		}

		return
	}

	successf(
		"SHA3-512 match.",
	)

	infof(
		"Package integrity verified.",
	)

	fmt.Println()

	localAppData := os.Getenv(
		"LOCALAPPDATA",
	)

	if localAppData == "" {
		errorf(
			"LOCALAPPDATA environment variable is not set",
		)
		return
	}

	installDir := filepath.Join(
		localAppData,
		"wpkm",
		"packages",
		packageName,
	)

	packagesDir := filepath.Dir(installDir)

	if err := os.MkdirAll(
		packagesDir,
		0755,
	); err != nil {
		errorf(
			"create package directory: %v",
			err,
		)
		return
	}

	stagingDir, err := os.MkdirTemp(
		packagesDir,
		"."+packageName+".wpkm-staging-*",
	)

	if err != nil {
		errorf(
			"create installation staging directory: %v",
			err,
		)
		return
	}

	stagingActive := true
	defer func() {
		if stagingActive {
			_ = os.RemoveAll(stagingDir)
		}
	}()

	isZIP := strings.EqualFold(
		filepath.Ext(manifest.Binary),
		".zip",
	)

	if isZIP {
		infof(
			"ZIP package detected. Extracting securely...",
		)

		if err := extractZIPSecure(
			tempBinary,
			stagingDir,
		); err != nil {
			errorf(
				"extract ZIP package: %v",
				err,
			)
			return
		}

		successf(
			"ZIP package extracted successfully.",
		)
	} else {
		installedBinary := filepath.Join(
			stagingDir,
			manifest.Binary,
		)

		if err := copyFileWithProgress(
			tempBinary,
			installedBinary,
		); err != nil {
			errorf(
				"stage package: %v",
				err,
			)
			return
		}
	}

	manifestPath := filepath.Join(
		stagingDir,
		"manifest.json",
	)

	if err := os.WriteFile(
		manifestPath,
		manifestData,
		0644,
	); err != nil {
		errorf(
			"stage manifest: %v",
			err,
		)
		return
	}

	hashPath := filepath.Join(
		stagingDir,
		"SHA3-512",
	)

	if err := os.WriteFile(
		hashPath,
		[]byte(actualHash+"\n"),
		0644,
	); err != nil {
		errorf(
			"stage SHA3-512 file: %v",
			err,
		)
		return
	}

	if err := replaceInstalledPackage(
		stagingDir,
		installDir,
	); err != nil {
		errorf(
			"install package: %v",
			err,
		)
		return
	}

	stagingActive = false

	if isZIP {
		successf(
			"ZIP package extracted and installed.",
		)
	}

	fmt.Println()

	fmt.Println(
		colorBold +
			colorGreen +
			"========================================" +
			colorReset,
	)

	fmt.Println(
		colorBold +
			colorGreen +
			" PACKAGE INSTALLED" +
			colorReset,
	)

	fmt.Println(
		colorBold +
			colorGreen +
			"========================================" +
			colorReset,
	)

	fmt.Printf(
		"Package: %s%s%s\n",
		colorMagenta,
		manifest.Name,
		colorReset,
	)

	fmt.Printf(
		"Version: %s%s%s\n",
		colorBlue,
		manifest.Version,
		colorReset,
	)

	fmt.Printf(
		"Location: %s%s%s\n",
		colorCyan,
		installDir,
		colorReset,
	)

	fmt.Printf(
		"SHA3-512: %s%s%s\n",
		colorBlue,
		actualHash,
		colorReset,
	)

	fmt.Println()

	successf(
		"Package installation successful.",
	)

	fmt.Println(
		colorBold +
			colorGreen +
			"========================================" +
			colorReset,
	)
}

// ------------------------------------------------------------
// ZIP INSTALLATION HELPERS
// ------------------------------------------------------------

func extractZIPSecure(
	zipPath string,
	destination string,
) error {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("open ZIP: %w", err)
	}
	defer reader.Close()

	cleanDestination, err := filepath.Abs(destination)
	if err != nil {
		return fmt.Errorf("resolve ZIP destination: %w", err)
	}

	for _, entry := range reader.File {
		name := strings.ReplaceAll(entry.Name, "\\", "/")

		if name == "" {
			continue
		}

		if strings.HasPrefix(name, "/") ||
			filepath.IsAbs(filepath.FromSlash(name)) {
			return fmt.Errorf("ZIP entry has an absolute path: %q", entry.Name)
		}

		cleanName := filepath.Clean(filepath.FromSlash(name))

		if cleanName == "." || cleanName == ".." ||
			strings.HasPrefix(cleanName, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("ZIP entry escapes installation directory: %q", entry.Name)
		}

		targetPath := filepath.Join(
			cleanDestination,
			cleanName,
		)

		relative, err := filepath.Rel(
			cleanDestination,
			targetPath,
		)
		if err != nil {
			return fmt.Errorf("validate ZIP entry %q: %w", entry.Name, err)
		}

		if relative == ".." ||
			strings.HasPrefix(relative, ".."+string(os.PathSeparator)) ||
			filepath.IsAbs(relative) {
			return fmt.Errorf("ZIP entry escapes installation directory: %q", entry.Name)
		}

		mode := entry.FileInfo().Mode()

		if mode&os.ModeSymlink != 0 {
			return fmt.Errorf("ZIP entry is a symlink and is not allowed: %q", entry.Name)
		}

		if mode&os.ModeNamedPipe != 0 ||
			mode&os.ModeSocket != 0 ||
			mode&os.ModeDevice != 0 ||
			mode&os.ModeCharDevice != 0 {
			return fmt.Errorf("ZIP entry is a special filesystem object and is not allowed: %q", entry.Name)
		}

		if entry.FileInfo().IsDir() || strings.HasSuffix(name, "/") {
			if err := os.MkdirAll(targetPath, 0755); err != nil {
				return fmt.Errorf("create ZIP directory %q: %w", entry.Name, err)
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return fmt.Errorf("create ZIP parent directory for %q: %w", entry.Name, err)
		}

		input, err := entry.Open()
		if err != nil {
			return fmt.Errorf("open ZIP entry %q: %w", entry.Name, err)
		}

		output, err := os.OpenFile(
			targetPath,
			os.O_WRONLY|os.O_CREATE|os.O_TRUNC,
			0644,
		)
		if err != nil {
			_ = input.Close()
			return fmt.Errorf("create extracted file %q: %w", entry.Name, err)
		}

		_, copyErr := io.Copy(output, input)
		closeOutputErr := output.Close()
		closeInputErr := input.Close()

		if copyErr != nil {
			return fmt.Errorf("extract ZIP entry %q: %w", entry.Name, copyErr)
		}

		if closeOutputErr != nil {
			return fmt.Errorf("close extracted file %q: %w", entry.Name, closeOutputErr)
		}

		if closeInputErr != nil {
			return fmt.Errorf("close ZIP entry %q: %w", entry.Name, closeInputErr)
		}
	}

	return nil
}

func replaceInstalledPackage(
	stagingDir string,
	installDir string,
) error {
	parentDir := filepath.Dir(installDir)
	backupDir := filepath.Join(
		parentDir,
		"."+filepath.Base(installDir)+".wpkm-backup-*",
	)

	var existing bool

	if _, err := os.Stat(installDir); err == nil {
		existing = true
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect existing package: %w", err)
	}

	if !existing {
		if err := os.Rename(stagingDir, installDir); err != nil {
			return fmt.Errorf("activate extracted package: %w", err)
		}
		return nil
	}

	backupPath, err := os.MkdirTemp(
		parentDir,
		backupDir,
	)
	if err != nil {
		return fmt.Errorf("create package backup directory: %w", err)
	}

	if err := os.Remove(backupPath); err != nil {
		return fmt.Errorf("prepare package backup path: %w", err)
	}

	if err := os.Rename(installDir, backupPath); err != nil {
		return fmt.Errorf("move existing package to backup: %w", err)
	}

	if err := os.Rename(stagingDir, installDir); err != nil {
		_ = os.Rename(backupPath, installDir)
		return fmt.Errorf("activate extracted package: %w", err)
	}

	if err := os.RemoveAll(backupPath); err != nil {
		warningf(
			"could not remove previous package backup: %v",
			err,
		)
	}

	return nil
}

// ------------------------------------------------------------
// GET HELPERS
// ------------------------------------------------------------

func registryPackageURL(
	packageName string,
	fileName string,
) string {
	return strings.TrimRight(
		registryRawBase,
		"/",
	) +
		"/" +
		url.PathEscape(packageName) +
		"/" +
		url.PathEscape(fileName)
}

func validateManifest(
	requestedPackage string,
	manifest registry.Manifest,
) error {
	if strings.TrimSpace(
		manifest.Name,
	) == "" {
		return fmt.Errorf(
			"manifest has no package name",
		)
	}

	if manifest.Name != requestedPackage {
		return fmt.Errorf(
			"manifest package name %q does not match %q",
			manifest.Name,
			requestedPackage,
		)
	}

	if strings.TrimSpace(
		manifest.Version,
	) == "" {
		return fmt.Errorf(
			"manifest has no version",
		)
	}

	if strings.TrimSpace(
		manifest.Binary,
	) == "" {
		return fmt.Errorf(
			"manifest has no binary filename",
		)
	}

	if filepath.Base(
		manifest.Binary,
	) != manifest.Binary ||
		filepath.IsAbs(
			manifest.Binary,
		) ||
		strings.ContainsAny(
			manifest.Binary,
			`/\`,
		) {
		return fmt.Errorf(
			"manifest binary must be a simple filename",
		)
	}

	expectedHash := strings.ToLower(
		strings.TrimSpace(
			manifest.SHA3_512,
		),
	)

	if len(expectedHash) != 128 {
		return fmt.Errorf(
			"manifest contains an invalid SHA3-512 hash",
		)
	}

	if _, err := hex.DecodeString(
		expectedHash,
	); err != nil {
		return fmt.Errorf(
			"manifest contains an invalid SHA3-512 hash",
		)
	}

	if !strings.EqualFold(
		manifest.Architecture,
		"amd64",
	) {
		return fmt.Errorf(
			"package architecture %q is not supported",
			manifest.Architecture,
		)
	}

	return nil
}

func downloadURL(
	targetURL string,
) ([]byte, error) {
	response, err := httpClient.Get(
		targetURL,
	)

	if err != nil {
		return nil, err
	}

	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(
			"HTTP %d %s",
			response.StatusCode,
			response.Status,
		)
	}

	return io.ReadAll(
		response.Body,
	)
}

func downloadToFileWithProgress(
	targetURL string,
	path string,
) error {
	response, err := httpClient.Get(
		targetURL,
	)

	if err != nil {
		return err
	}

	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf(
			"HTTP %d %s",
			response.StatusCode,
			response.Status,
		)
	}

	file, err := os.Create(path)

	if err != nil {
		return err
	}

	total := response.ContentLength

	var downloaded int64

	buffer := make([]byte, 64*1024)

	offset := 0
	lastDraw := time.Now()

	for {
		n, readErr := response.Body.Read(buffer)

		if n > 0 {
			written, writeErr := file.Write(
				buffer[:n],
			)

			if writeErr != nil {
				_ = file.Close()
				_ = os.Remove(path)
				return writeErr
			}

			if written != n {
				_ = file.Close()
				_ = os.Remove(path)
				return io.ErrShortWrite
			}

			downloaded += int64(n)

			if time.Since(lastDraw) >=
				50*time.Millisecond {

				if total > 0 {
					renderProgress(
						"Downloading...",
						float64(downloaded)/
							float64(total),
						offset,
					)
				} else {
					renderProgress(
						"Downloading...",
						float64(
							(downloaded/(64*1024))%100+1,
						)/100.0,
						offset,
					)
				}

				offset++
				lastDraw = time.Now()
			}
		}

		if readErr != nil {
			if readErr != io.EOF {
				_ = file.Close()
				_ = os.Remove(path)
				return readErr
			}

			break
		}
	}

	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}

	renderProgress(
		"Downloading...",
		1,
		offset,
	)

	finishProgress(
		"Download complete.",
	)

	return nil
}

func copyFileWithProgress(
	source string,
	destination string,
) error {
	input, err := os.Open(source)

	if err != nil {
		return err
	}

	defer input.Close()

	info, err := input.Stat()

	if err != nil {
		return err
	}

	output, err := os.Create(destination)

	if err != nil {
		return err
	}

	buffer := make([]byte, 64*1024)

	var copied int64

	offset := 0
	lastDraw := time.Now()

	for {
		n, readErr := input.Read(buffer)

		if n > 0 {
			written, writeErr := output.Write(
				buffer[:n],
			)

			if writeErr != nil {
				_ = output.Close()
				_ = os.Remove(destination)
				return writeErr
			}

			if written != n {
				_ = output.Close()
				_ = os.Remove(destination)
				return io.ErrShortWrite
			}

			copied += int64(n)

			if time.Since(lastDraw) >=
				50*time.Millisecond {

				fraction := 0.0

				if info.Size() > 0 {
					fraction =
						float64(copied) /
							float64(info.Size())
				}

				renderProgress(
					"Installing...",
					fraction,
					offset,
				)

				offset++
				lastDraw = time.Now()
			}
		}

		if readErr != nil {
			if readErr != io.EOF {
				_ = output.Close()
				_ = os.Remove(destination)
				return readErr
			}

			break
		}
	}

	if err := output.Close(); err != nil {
		_ = os.Remove(destination)
		return err
	}

	renderProgress(
		"Installing...",
		1,
		offset,
	)

	finishProgress(
		"Installation copy complete.",
	)

	return nil
}

// ------------------------------------------------------------
// PURGE
// ------------------------------------------------------------

func handlePurge(args []string) {
	if len(args) < 1 {
		errorf("Usage: wpkm purge <package>")
		return
	}

	packageName := strings.TrimSpace(
		strings.Join(args, " "),
	)

	if packageName == "" {
		errorf("Usage: wpkm purge <package>")
		return
	}

	fmt.Println()

	fmt.Printf(
		"%sRemoving package:%s '%s'\n",
		colorBold+colorMagenta,
		colorReset,
		packageName,
	)

	fmt.Println()

	localAppData := os.Getenv(
		"LOCALAPPDATA",
	)

	if localAppData == "" {
		errorf(
			"LOCALAPPDATA environment variable is not set",
		)
		return
	}

	baseDir := filepath.Join(
		localAppData,
		"wpkm",
		"packages",
	)

	installDir := filepath.Join(
		baseDir,
		packageName,
	)

	cleanBase := filepath.Clean(baseDir)
	cleanInstall := filepath.Clean(installDir)

	relative, err := filepath.Rel(
		cleanBase,
		cleanInstall,
	)

	if err != nil {
		errorf(
			"determine package path: %v",
			err,
		)
		return
	}

	if relative == ".." ||
		strings.HasPrefix(
			relative,
			".."+string(os.PathSeparator),
		) ||
		filepath.IsAbs(relative) {
		errorf("invalid package name")
		return
	}

	info, err := os.Stat(cleanInstall)

	if err != nil {
		if os.IsNotExist(err) {
			warningf(
				"package '%s' is not installed",
				packageName,
			)
			return
		}

		errorf(
			"cannot access installed package: %v",
			err,
		)

		return
	}

	if !info.IsDir() {
		errorf(
			"installed package path is not a directory: %s",
			cleanInstall,
		)
		return
	}

	fmt.Printf(
		"Location: %s%s%s\n",
		colorCyan,
		cleanInstall,
		colorReset,
	)

	fmt.Println()

	stopProgress := startIndeterminateProgress(
		"Deleting package...",
	)

	err = os.RemoveAll(
		cleanInstall,
	)

	stopProgress()

	if err != nil {
		errorf(
			"remove package: %v",
			err,
		)
		return
	}

	_ = os.Remove(
		filepath.Join(
			localAppData,
			"wpkm",
			"packages",
		),
	)

	_ = os.Remove(
		filepath.Join(
			localAppData,
			"wpkm",
		),
	)

	fmt.Println()

	fmt.Println(
		colorBold +
			colorGreen +
			"========================================" +
			colorReset,
	)

	fmt.Println(
		colorBold +
			colorGreen +
			" PACKAGE PURGED" +
			colorReset,
	)

	fmt.Println(
		colorBold +
			colorGreen +
			"========================================" +
			colorReset,
	)

	fmt.Printf(
		"Package: %s%s%s\n",
		colorMagenta,
		packageName,
		colorReset,
	)

	fmt.Printf(
		"Removed: %s%s%s\n",
		colorCyan,
		cleanInstall,
		colorReset,
	)

	fmt.Println()

	successf(
		"Package removal successful.",
	)

	fmt.Println(
		colorBold +
			colorGreen +
			"========================================" +
			colorReset,
	)
}

// ------------------------------------------------------------
// ASK
// ------------------------------------------------------------

func handleAsk(args []string) {
	if len(args) < 1 {
		errorf("Usage: wpkm ask <package>")
		return
	}

	query := strings.TrimSpace(
		strings.Join(args, " "),
	)

	if query == "" {
		errorf("Usage: wpkm ask <package>")
		return
	}

	fmt.Println()

	fmt.Printf(
		"%sSearching WPKM packages for:%s '%s'\n",
		colorBold+colorMagenta,
		colorReset,
		query,
	)

	fmt.Println()

	infof(
		"Fetching package registry...",
	)

	stopProgress := startIndeterminateProgress(
		"Searching registry...",
	)

	contents, err := fetchRegistryPackages()

	stopProgress()

	if err != nil {
		errorf(
			"cannot access package registry: %v",
			err,
		)
		return
	}

	queryLower := strings.ToLower(query)

	var matches []githubContent

	for _, item := range contents {
		if item.Type != "dir" {
			continue
		}

		if strings.Contains(
			strings.ToLower(item.Name),
			queryLower,
		) {
			matches = append(
				matches,
				item,
			)
		}
	}

	if len(matches) == 0 {
		warningf(
			"No packages found matching '%s'.",
			query,
		)
		return
	}

	plural := ""

	if len(matches) != 1 {
		plural = "s"
	}

	fmt.Printf(
		"%sFound %d package%s:%s\n\n",
		colorBold+colorGreen,
		len(matches),
		plural,
		colorReset,
	)

	for _, match := range matches {
		fmt.Println(
			colorBold +
				colorMagenta +
				"----------------------------------------" +
				colorReset,
		)

		fmt.Printf(
			"%s%s%s\n",
			colorBold+colorMagenta,
			match.Name,
			colorReset,
		)

		stopProgress = startIndeterminateProgress(
			"Reading manifest...",
		)

		manifest, err := fetchPackageManifest(
			match.Name,
		)

		stopProgress()

		if err != nil {
			warningf(
				"Could not read manifest for '%s': %v",
				match.Name,
				err,
			)

			fmt.Println()
			continue
		}

		fmt.Printf(
			"Version: %s%s%s\n",
			colorBlue,
			manifest.Version,
			colorReset,
		)

		fmt.Printf(
			"Architecture: %s%s%s\n",
			colorBlue,
			manifest.Architecture,
			colorReset,
		)

		fmt.Printf(
			"Binary: %s%s%s\n",
			colorCyan,
			manifest.Binary,
			colorReset,
		)

		if strings.TrimSpace(
			manifest.Description,
		) != "" {
			fmt.Printf(
				"Description: %s\n",
				manifest.Description,
			)
		}

		fmt.Println()
	}

	fmt.Println(
		colorBold +
			colorMagenta +
			"----------------------------------------" +
			colorReset,
	)

	successf(
		"Search complete.",
	)
}

// ------------------------------------------------------------
// ASK HELPERS
// ------------------------------------------------------------

func fetchRegistryPackages() (
	[]githubContent,
	error,
) {
	targetURL := githubAPIBase +
		"/contents/packages"

	requestRegistry := func(token string) (*http.Response, error) {
		request, err := http.NewRequest(
			http.MethodGet,
			targetURL,
			nil,
		)
		if err != nil {
			return nil, err
		}

		request.Header.Set(
			"Accept",
			"application/vnd.github+json",
		)
		request.Header.Set(
			"X-GitHub-Api-Version",
			"2026-03-10",
		)
		request.Header.Set(
			"User-Agent",
			"wpkm/"+version,
		)

		if token != "" {
			request.Header.Set(
				"Authorization",
				"Bearer "+token,
			)
		}

		return httpClient.Do(request)
	}

	token := strings.TrimSpace(
		os.Getenv("WPKM_GITHUB_TOKEN"),
	)

	response, err := requestRegistry(token)
	if err != nil {
		return nil, err
	}

	if response.StatusCode == http.StatusForbidden ||
		response.StatusCode == http.StatusTooManyRequests {
		_ = response.Body.Close()

		// The public GitHub API is limited to a small number of requests
		// per hour. If the anonymous request is rate-limited, reuse the
		// same stored GitHub credential used by `wpkm push` and retry once.
		if token == "" {
			token, err = auth.GetOrPrompt()
			if err != nil {
				return nil, fmt.Errorf(
					"GitHub API rate limit reached (HTTP %d); authentication is required for registry search: %w",
					response.StatusCode,
					err,
				)
			}

			token = strings.TrimSpace(token)
		}

		if token != "" {
			response, err = requestRegistry(token)
			if err != nil {
				return nil, err
			}
		}
	}

	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(
			"HTTP %d %s",
			response.StatusCode,
			response.Status,
		)
	}

	var contents []githubContent

	if err := json.NewDecoder(
		response.Body,
	).Decode(&contents); err != nil {
		return nil, err
	}

	return contents, nil
}

func fetchPackageManifest(
	packageName string,
) (registry.Manifest, error) {
	targetURL := registryPackageURL(
		packageName,
		"manifest.json",
	)

	data, err := downloadURL(
		targetURL,
	)

	if err != nil {
		return registry.Manifest{}, err
	}

	var manifest registry.Manifest

	if err := json.Unmarshal(
		data,
		&manifest,
	); err != nil {
		return registry.Manifest{}, err
	}

	return manifest, nil
}

// ------------------------------------------------------------
// WHERE
// ------------------------------------------------------------

func handleWhere(args []string) {
	if len(args) < 1 {
		errorf("Usage: wpkm where <package>")
		return
	}

	packageName := strings.TrimSpace(
		strings.Join(args, " "),
	)

	if packageName == "" {
		errorf("Usage: wpkm where <package>")
		return
	}

	fmt.Println()

	fmt.Printf(
		"%sLooking for installed package:%s '%s'\n",
		colorBold+colorMagenta,
		colorReset,
		packageName,
	)

	fmt.Println()

	localAppData := os.Getenv(
		"LOCALAPPDATA",
	)

	if localAppData == "" {
		errorf(
			"LOCALAPPDATA environment variable is not set",
		)
		return
	}

	installDir := filepath.Join(
		localAppData,
		"wpkm",
		"packages",
		packageName,
	)

	info, err := os.Stat(installDir)

	if err != nil {
		if os.IsNotExist(err) {
			warningf(
				"package '%s' is not installed",
				packageName,
			)
			return
		}

		errorf(
			"cannot access installed package: %v",
			err,
		)

		return
	}

	if !info.IsDir() {
		errorf(
			"package installation path is not a directory: %s",
			installDir,
		)
		return
	}

	fmt.Println(
		colorBold +
			colorCyan +
			"========================================" +
			colorReset,
	)

	fmt.Println(
		colorBold +
			colorCyan +
			" PACKAGE LOCATION" +
			colorReset,
	)

	fmt.Println(
		colorBold +
			colorCyan +
			"========================================" +
			colorReset,
	)

	fmt.Printf(
		"Package: %s%s%s\n",
		colorMagenta,
		packageName,
		colorReset,
	)

	fmt.Printf(
		"Location: %s%s%s\n",
		colorCyan,
		installDir,
		colorReset,
	)

	fmt.Println(
		colorBold +
			colorCyan +
			"========================================" +
			colorReset,
	)
}

// ------------------------------------------------------------
// LIST
// ------------------------------------------------------------

func handleList(args []string) {
	if len(args) > 0 {
		errorf("Usage: wpkm list")
		return
	}

	localAppData := os.Getenv(
		"LOCALAPPDATA",
	)

	if localAppData == "" {
		errorf(
			"LOCALAPPDATA environment variable is not set",
		)
		return
	}

	packagesDir := filepath.Join(
		localAppData,
		"wpkm",
		"packages",
	)

	entries, err := os.ReadDir(packagesDir)

	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println()

			infof(
				"No packages are currently installed.",
			)

			return
		}

		errorf(
			"read installed packages: %v",
			err,
		)

		return
	}

	var packages []string

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		packages = append(
			packages,
			entry.Name(),
		)
	}

	sort.Strings(packages)

	fmt.Println()

	fmt.Println(
		colorBold +
			colorCyan +
			"========================================" +
			colorReset,
	)

	fmt.Println(
		colorBold +
			colorCyan +
			" INSTALLED PACKAGES" +
			colorReset,
	)

	fmt.Println(
		colorBold +
			colorCyan +
			"========================================" +
			colorReset,
	)

	if len(packages) == 0 {
		fmt.Println()

		infof(
			"No packages are currently installed.",
		)

		fmt.Println()

		fmt.Println(
			colorBold +
				colorCyan +
				"========================================" +
				colorReset,
		)

		return
	}

	fmt.Println()

	for _, packageName := range packages {
		manifestPath := filepath.Join(
			packagesDir,
			packageName,
			"manifest.json",
		)

		versionText := "unknown"

		data, err := os.ReadFile(
			manifestPath,
		)

		if err == nil {
			var manifest registry.Manifest

			if json.Unmarshal(
				data,
				&manifest,
			) == nil &&
				strings.TrimSpace(
					manifest.Version,
				) != "" {

				versionText = manifest.Version
			}
		}

		fmt.Printf(
			"  %s%-24s%s %s%s%s\n",
			colorMagenta+colorBold,
			packageName,
			colorReset,
			colorBlue,
			versionText,
			colorReset,
		)
	}

	fmt.Println()

	fmt.Printf(
		"%s%d package%s installed.%s\n",
		colorGreen+colorBold,
		len(packages),
		func() string {
			if len(packages) == 1 {
				return ""
			}
			return "s"
		}(),
		colorReset,
	)

	fmt.Println()

	fmt.Println(
		colorBold +
			colorCyan +
			"========================================" +
			colorReset,
	)
}

// ------------------------------------------------------------
// VERSION
// ------------------------------------------------------------

func handleVersion(args []string) {
	// wpkm version
	// Shows the core WPKM version.
	if len(args) == 0 {
		fmt.Printf(
			"%swpkm %s%s\n",
			colorBold+colorCyan,
			version,
			colorReset,
		)

		return
	}

	// wpkm version <package>
	// Shows the installed package version.
	packageName := strings.TrimSpace(
		strings.Join(args, " "),
	)

	if packageName == "" {
		fmt.Printf(
			"%swpkm %s%s\n",
			colorBold+colorCyan,
			version,
			colorReset,
		)

		return
	}

	localAppData := os.Getenv(
		"LOCALAPPDATA",
	)

	if localAppData == "" {
		errorf(
			"LOCALAPPDATA environment variable is not set",
		)
		return
	}

	installDir := filepath.Join(
		localAppData,
		"wpkm",
		"packages",
		packageName,
	)

	manifestPath := filepath.Join(
		installDir,
		"manifest.json",
	)

	data, err := os.ReadFile(
		manifestPath,
	)

	if err != nil {
		if os.IsNotExist(err) {
			warningf(
				"package '%s' is not installed",
				packageName,
			)
			return
		}

		errorf(
			"read package manifest: %v",
			err,
		)

		return
	}

	var manifest registry.Manifest

	if err := json.Unmarshal(
		data,
		&manifest,
	); err != nil {
		errorf(
			"invalid manifest for '%s': %v",
			packageName,
			err,
		)

		return
	}

	if strings.TrimSpace(
		manifest.Version,
	) == "" {
		errorf(
			"package '%s' has no version in its manifest",
			packageName,
		)

		return
	}

	fmt.Printf(
		"%s%s%s %s%s%s\n",
		colorBold+colorMagenta,
		manifest.Name,
		colorReset,
		colorBold+colorBlue,
		manifest.Version,
		colorReset,
	)
}

// ------------------------------------------------------------
// ABOUT
// ------------------------------------------------------------

func handleAbout(args []string) {
	// wpkm about
	// Shows information about WPKM itself.
	if len(args) == 0 {
		fmt.Println()

		fmt.Println(
			colorBold +
				colorCyan +
				"wpkm" +
				colorReset,
		)

		fmt.Println(
			"Windows Package Manager",
		)

		fmt.Println()

		fmt.Printf(
			"Version: %s%s%s\n",
			colorBlue,
			version,
			colorReset,
		)

		fmt.Println(
			"Lightweight package management for Windows.",
		)

		fmt.Println()

		fmt.Println("Commands:")
		fmt.Println("  push     Publish a package")
		fmt.Println("  get      Install a package")
		fmt.Println("  purge    Remove a package")
		fmt.Println("  ask      Search for a package")
		fmt.Println("  list     List installed packages")
		fmt.Println("  where    Show installed package location")
		fmt.Println("  about    Show information")
		fmt.Println("  help     Show help")
		fmt.Println("  version  Show version")

		fmt.Println()

		return
	}

	// wpkm about <package>
	// Shows information about an installed package.
	packageName := strings.TrimSpace(
		strings.Join(args, " "),
	)

	if packageName == "" {
		handleAbout(nil)
		return
	}

	localAppData := os.Getenv(
		"LOCALAPPDATA",
	)

	if localAppData == "" {
		errorf(
			"LOCALAPPDATA environment variable is not set",
		)
		return
	}

	installDir := filepath.Join(
		localAppData,
		"wpkm",
		"packages",
		packageName,
	)

	manifestPath := filepath.Join(
		installDir,
		"manifest.json",
	)

	data, err := os.ReadFile(
		manifestPath,
	)

	if err != nil {
		if os.IsNotExist(err) {
			warningf(
				"package '%s' is not installed",
				packageName,
			)
			return
		}

		errorf(
			"read package manifest: %v",
			err,
		)

		return
	}

	var manifest registry.Manifest

	if err := json.Unmarshal(
		data,
		&manifest,
	); err != nil {
		errorf(
			"invalid manifest for '%s': %v",
			packageName,
			err,
		)

		return
	}

	fmt.Println()

	fmt.Println(
		colorBold +
			colorMagenta +
			"========================================" +
			colorReset,
	)

	fmt.Printf(
		"%s%s%s\n",
		colorBold+colorMagenta,
		manifest.Name,
		colorReset,
	)

	fmt.Println(
		colorBold +
			colorMagenta +
			"========================================" +
			colorReset,
	)

	fmt.Println()

	fmt.Printf(
		"Version:      %s%s%s\n",
		colorBlue,
		manifest.Version,
		colorReset,
	)

	fmt.Printf(
		"Architecture: %s%s%s\n",
		colorBlue,
		manifest.Architecture,
		colorReset,
	)

	fmt.Printf(
		"Binary:       %s%s%s\n",
		colorCyan,
		manifest.Binary,
		colorReset,
	)

	if strings.TrimSpace(
		manifest.Description,
	) != "" {
		fmt.Printf(
			"Description:  %s\n",
			manifest.Description,
		)
	}

	fmt.Printf(
		"Location:     %s%s%s\n",
		colorCyan,
		installDir,
		colorReset,
	)

	fmt.Printf(
		"SHA3-512:     %s%s%s\n",
		colorBlue,
		manifest.SHA3_512,
		colorReset,
	)

	fmt.Println()

	fmt.Println(
		colorBold +
			colorMagenta +
			"========================================" +
			colorReset,
	)
}

// ------------------------------------------------------------
// HELP
// ------------------------------------------------------------

func printHelp() {
	fmt.Println()

	fmt.Println(
		colorBold +
			colorCyan +
			"wpkm - Windows Package Manager" +
			colorReset,
	)

	fmt.Println()

	fmt.Println("Usage:")
	fmt.Println(
		"  wpkm <command> [arguments]",
	)

	fmt.Println()

	fmt.Println("Commands:")
	fmt.Println()

	fmt.Println("  push <file>")
	fmt.Println(
		"      Publish a package to the WPKM repository.",
	)

	fmt.Println()

	fmt.Println("  get <package>")
	fmt.Println(
		"      Install the latest version of a package.",
	)

	fmt.Println()

	fmt.Println("  purge <package>")
	fmt.Println(
		"      Remove an installed package.",
	)

	fmt.Println()

	fmt.Println("  ask <package>")
	fmt.Println(
		"      Search for a package in the registry.",
	)

	fmt.Println()

	fmt.Println("  list")
	fmt.Println(
		"      List installed packages.",
	)

	fmt.Println()

	fmt.Println("  where <package>")
	fmt.Println(
		"      Show where a package is installed.",
	)

	fmt.Println()

	fmt.Println("  about [package]")
	fmt.Println(
		"      Show information about WPKM or an installed package.",
	)

	fmt.Println()

	fmt.Println("  version [package]")
	fmt.Println(
		"      Show the WPKM or installed package version.",
	)

	fmt.Println()

	fmt.Println("  help")
	fmt.Println(
		"      Show this help message.",
	)

	fmt.Println()
}
