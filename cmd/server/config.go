package main

// config.go previously held a hand-rolled config struct and mustEnv helpers.
// All environment variable handling is now inline in main.go via envOr() and
// envInt() (following the reference iam-org-membership pattern). This file is
// kept as a package-level no-op so the build does not break if the filename
// persists in git history.
