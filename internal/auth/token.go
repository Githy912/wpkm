//go:build windows

package auth

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	credentialTarget = "wpkm.github.com"
	credentialUser   = "wpkm"

	credTypeGeneric = 1

	credPersistLocalMachine = 2

	errorNotFound syscall.Errno = 1168

	enableProcessedInput = 0x0001
	enableLineInput      = 0x0002
	enableEchoInput      = 0x0004
)

var (
	advapi32 = windows.NewLazySystemDLL("advapi32.dll")

	procCredReadW  = advapi32.NewProc("CredReadW")
	procCredWriteW = advapi32.NewProc("CredWriteW")
	procCredFree   = advapi32.NewProc("CredFree")

	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procGetStdHandle      = kernel32.NewProc("GetStdHandle")
	procGetConsoleMode    = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode    = kernel32.NewProc("SetConsoleMode")
	procReadConsoleW      = kernel32.NewProc("ReadConsoleW")
	procFlushConsoleInput = kernel32.NewProc("FlushConsoleInputBuffer")
)

type credential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        windows.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

var errCredentialNotFound = errors.New(
	"WPKM GitHub credential not found",
)

func Get() (string, error) {
	if token := strings.TrimSpace(
		os.Getenv("WPKM_GITHUB_TOKEN"),
	); token != "" {
		return token, nil
	}

	token, err := readCredential()
	if err != nil {
		return "", err
	}

	return token, nil
}

func GetOrPrompt() (string, error) {
	token, err := Get()

	if err == nil {
		return token, nil
	}

	if !errors.Is(err, errCredentialNotFound) {
		return "", err
	}

	fmt.Println()
	fmt.Println("No GitHub token is saved for WPKM.")
	fmt.Println("The token will be stored in Windows Credential Manager.")
	fmt.Println()

	token, err = Prompt()
	if err != nil {
		return "", err
	}

	if err := Save(token); err != nil {
		return "", fmt.Errorf(
			"save GitHub token: %w",
			err,
		)
	}

	fmt.Println("GitHub token saved securely.")

	return token, nil
}

func Save(token string) error {
	token = strings.TrimSpace(token)

	if token == "" {
		return errors.New("empty GitHub token")
	}

	targetName, err := windows.UTF16PtrFromString(
		credentialTarget,
	)
	if err != nil {
		return fmt.Errorf(
			"encode credential target: %w",
			err,
		)
	}

	userName, err := windows.UTF16PtrFromString(
		credentialUser,
	)
	if err != nil {
		return fmt.Errorf(
			"encode credential username: %w",
			err,
		)
	}

	blob := []byte(token)

	cred := credential{
		Type:               credTypeGeneric,
		TargetName:         targetName,
		CredentialBlobSize: uint32(len(blob)),
		Persist:            credPersistLocalMachine,
		UserName:           userName,
	}

	if len(blob) > 0 {
		cred.CredentialBlob = &blob[0]
	}

	ret, _, callErr := procCredWriteW.Call(
		uintptr(unsafe.Pointer(&cred)),
		0,
	)

	if ret == 0 {
		if callErr != nil {
			return fmt.Errorf(
				"CredWriteW failed: %w",
				callErr,
			)
		}

		return errors.New("CredWriteW failed")
	}

	return nil
}

func readCredential() (string, error) {
	targetName, err := windows.UTF16PtrFromString(
		credentialTarget,
	)
	if err != nil {
		return "", fmt.Errorf(
			"encode credential target: %w",
			err,
		)
	}

	var credPtr *credential

	ret, _, callErr := procCredReadW.Call(
		uintptr(unsafe.Pointer(targetName)),
		credTypeGeneric,
		0,
		uintptr(unsafe.Pointer(&credPtr)),
	)

	if ret == 0 {
		if callErr != nil {
			if errors.Is(callErr, errorNotFound) {
				return "", errCredentialNotFound
			}

			return "", fmt.Errorf(
				"CredReadW failed: %w",
				callErr,
			)
		}

		return "", errors.New("CredReadW failed")
	}

	if credPtr == nil {
		return "", errors.New(
			"CredReadW returned a nil credential",
		)
	}

	defer func() {
		procCredFree.Call(
			uintptr(unsafe.Pointer(credPtr)),
		)
	}()

	cred := *credPtr

	if cred.CredentialBlobSize == 0 ||
		cred.CredentialBlob == nil {
		return "", errors.New(
			"stored GitHub credential is empty",
		)
	}

	blob := unsafe.Slice(
		cred.CredentialBlob,
		cred.CredentialBlobSize,
	)

	token := strings.TrimSpace(
		string(blob),
	)

	if token == "" {
		return "", errors.New(
			"stored GitHub credential is empty",
		)
	}

	return token, nil
}

func Prompt() (string, error) {
	fmt.Print("GitHub token: ")

	token, err := readHiddenInput()
	if err != nil {
		return "", err
	}

	fmt.Println()

	token = strings.TrimSpace(token)

	if token == "" {
		return "", errors.New(
			"empty GitHub token",
		)
	}

	return token, nil
}

func readHiddenInput() (string, error) {
	const stdInputHandle = ^uintptr(9)

	handle, _, callErr := procGetStdHandle.Call(
		stdInputHandle,
	)

	if handle == 0 || handle == ^uintptr(0) {
		return "", fmt.Errorf(
			"GetStdHandle failed: %w",
			callErr,
		)
	}

	var originalMode uint32

	ret, _, callErr := procGetConsoleMode.Call(
		handle,
		uintptr(unsafe.Pointer(&originalMode)),
	)

	if ret == 0 {
		return "", fmt.Errorf(
			"GetConsoleMode failed: %w",
			callErr,
		)
	}

	newMode := originalMode

	newMode |= enableProcessedInput
	newMode |= enableLineInput
	newMode &^= enableEchoInput

	ret, _, callErr = procSetConsoleMode.Call(
		handle,
		uintptr(newMode),
	)

	if ret == 0 {
		return "", fmt.Errorf(
			"SetConsoleMode failed: %w",
			callErr,
		)
	}

	defer func() {
		procSetConsoleMode.Call(
			handle,
			uintptr(originalMode),
		)
	}()

	ret, _, callErr = procFlushConsoleInput.Call(handle)

	if ret == 0 {
		return "", fmt.Errorf(
			"FlushConsoleInputBuffer failed: %w",
			callErr,
		)
	}

	buffer := make([]uint16, 512)

	var read uint32

	ret, _, callErr = procReadConsoleW.Call(
		handle,
		uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(len(buffer)),
		uintptr(unsafe.Pointer(&read)),
		0,
	)

	if ret == 0 {
		return "", fmt.Errorf(
			"ReadConsoleW failed: %w",
			callErr,
		)
	}

	input := windows.UTF16ToString(
		buffer[:read],
	)

	input = strings.TrimRight(
		input,
		"\r\n",
	)

	return input, nil
}
