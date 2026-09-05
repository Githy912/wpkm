# Read this if you wanna build
Go to Powershell 5.1 or later then:

1. `cd <PATH_TO_WPKM_SOURCE>`
2. `go version`
3. Verify modules:
```bash
go env GOOS
go env GOARCH
```
4. `go clean`
5. If an old executable exists, `Remove-Item .\wpkm.exe -Force -ErrorAction SilentlyContinue`
6. ```bash
    gofmt -w .\cmd\wpkm\main.go
    gofmt -w .\internal\auth\token.go
    gofmt -w .\internal\hash\hash.go
    gofmt -w .\internal\registry\github.go
   ```
7. `go mod tidy`
8. `go mod download`
9. `go test ./...`
10. `go vet ./...`
11. `go build -o .\wpkm.exe .\cmd\wpkm`
12. ENJOY! IF TOO LAZY TO COPY AND PASTE, RUN build.ps1!
