# Sample project notes

Plain lines like this one are never judged.

- Login is handled in the auth package <!-- plumb: exists src/auth ; grep "func Login" src/auth -->
- The legacy billing module was removed <!-- plumb: absent src/billing -->
- Login's signature is pinned <!-- plumb: hash src/auth/login.go:3-5 072740ab -->
- The auth package also exposes `Login` and lives in `src/auth/`
- We use Go for everything
