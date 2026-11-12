#!/bin/sh
# Wire git to the GH_TOKEN env var, set identity, then run the given command.
if [ -n "$GH_TOKEN" ]; then gh auth setup-git 2>/dev/null; fi
if [ -n "$NIGHTSHIFT_GIT_NAME" ]; then
  git config --global user.name "$NIGHTSHIFT_GIT_NAME"
  git config --global user.email "$NIGHTSHIFT_GIT_EMAIL"
fi
git config --global --add safe.directory '*'
exec "$@"
