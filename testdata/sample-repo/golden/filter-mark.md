# Sample project notes

Plain lines like this one are never judged.

- Login is handled in the auth package <!-- plumb: exists src/auth ; grep "func Login" src/auth -->
- [STALE] Config lives in config/app.yaml <!-- plumb: exists config/app.yaml -->
- The legacy billing module was removed <!-- plumb: absent src/billing -->
- [STALE] HTTP retries are capped by maxRetries <!-- plumb: grep "maxRetries" src/http -->
- Login's signature is pinned <!-- plumb: hash src/auth/login.go:3-5 072740ab -->
- [STALE] The HTTP client file is unchanged <!-- plumb: hash src/http/client.go deadbeef -->
- [STALE] A typo in the evidence kind <!-- plumb: frobnicate src/auth -->
- The auth package also exposes `Login` and lives in `src/auth/`
- We use Go for everything
