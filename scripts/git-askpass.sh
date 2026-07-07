#!/usr/bin/env sh
# GIT_ASKPASS helper for konveyor-push. Git calls this for both username
# and password prompts; returning KONVEYOR_GIT_TOKEN for either works for
# token-based auth (GitHub/GitLab personal access tokens accept the token
# as the password with any non-empty username).
echo "$KONVEYOR_GIT_TOKEN"
