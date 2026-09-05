package hash

import (
"crypto/sha3"
"encoding/hex"
"fmt"
"io"
"os"
)

// File calculates the SHA3-512 hash of a file
// and returns it as a lowercase hexadecimal string.
func File(path string) (string, error) {
f, err := os.Open(path)
if err != nil {
return "", fmt.Errorf("open file: %w", err)
}
defer f.Close()

h := sha3.New512()

if _, err := io.Copy(h, f); err != nil {
return "", fmt.Errorf("hash file: %w", err)
}

return hex.EncodeToString(h.Sum(nil)), nil
}