#!/usr/bin/env bash
# Deploy workflows to a single branch
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/common.sh"

AUTOMATION_CORE_DIR=$(get_automation_core_dir)

require_var BRANCH
require_var CONFIG_FILE
require_var TARGET_DIR

# Build renderer if not exists
if [[ ! -f "$AUTOMATION_CORE_DIR/render-templates" ]]; then
  echo "Building template renderer..."
  cd "$AUTOMATION_CORE_DIR"
  CGO_ENABLED=0 go build -o render-templates ./render-templates.go
fi

# Render templates
STAGING_DIR=$(mktemp -d)
echo "Rendering templates to $STAGING_DIR..."
"$AUTOMATION_CORE_DIR/render-templates" \
  -config "$AUTOMATION_CORE_DIR/$CONFIG_FILE" \
  -template-dir "$AUTOMATION_CORE_DIR/templates" \
  -output-dir "$STAGING_DIR"

# Fetch and checkout target branch
cd "$TARGET_DIR"
git fetch "$REMOTE"
git checkout "$REMOTE/$BRANCH" || git checkout "$BRANCH"

# Show diffs if dry-run
if [[ "$DRY_RUN" == "true" ]]; then
  summary "### Diff Preview"
  shopt -s nullglob
  for workflow in "$STAGING_DIR"/*.yaml "$STAGING_DIR"/*.yml; do
    [[ -f "$workflow" ]] || continue
    filename=$(basename "$workflow")
    summary "#### $filename"
    summary '```diff'
    if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
      diff -u ".github/workflows/$filename" "$workflow" >> "$GITHUB_STEP_SUMMARY" 2>&1 || true
    fi
    summary '```'

    # Also show on stdout
    echo "=== $filename ==="
    diff -u ".github/workflows/$filename" "$workflow" || true
  done
  shopt -u nullglob
  summary ""
  exit 0
fi

# Create deployment branch
DEPLOY_BRANCH="deploy-workflows-$(date +%Y-%m-%d-%H-%M-%S)"
git checkout -b "$DEPLOY_BRANCH"

# Copy workflows
mkdir -p .github/workflows
shopt -s nullglob
cp "$STAGING_DIR"/*.yaml "$STAGING_DIR"/*.yml .github/workflows/ 2>/dev/null || true
shopt -u nullglob
git add .github/workflows/

# Check for changes
if git diff --cached --quiet; then
  echo "No changes - workflows already up to date"
  summary "- ✓ No changes needed"
  summary ""
  return 0
fi

# Generate commit message
COMMIT_MSG_FILE=$(mktemp)
bash "$AUTOMATION_CORE_DIR/.github/scripts/generate-commit-msg.sh" \
  "$BRANCH" \
  "$K3S_VERSIONS" \
  "$AUTOMATION_CORE_REF" \
  "$(git -C "$AUTOMATION_CORE_DIR" rev-parse HEAD)" > "$COMMIT_MSG_FILE"

# Commit
git commit -F "$COMMIT_MSG_FILE"
rm "$COMMIT_MSG_FILE"

# Push (unless in GHA where PR creation handles it)
if [[ -z "${GITHUB_ACTIONS:-}" ]]; then
  echo "Pushing to $REMOTE/$DEPLOY_BRANCH..."
  git push "$REMOTE" "$DEPLOY_BRANCH"
  
  # Generate PR body
  PR_BODY_FILE=$(mktemp)
  bash "$AUTOMATION_CORE_DIR/.github/scripts/generate-pr-body.sh" \
    "$BRANCH" \
    "$K3S_VERSIONS" \
    "$AUTOMATION_CORE_REF" \
    "$(git -C "$AUTOMATION_CORE_DIR" rev-parse HEAD)" \
    "${USER:-unknown}" > "$PR_BODY_FILE"
  
  # Create PR with gh CLI
  if command -v gh &> /dev/null; then
    echo "Creating PR..."
    PR_URL=$(gh pr create \
      --base "$BRANCH" \
      --head "$DEPLOY_BRANCH" \
      --title "Deploy automation-core workflows to $BRANCH" \
      --body-file "$PR_BODY_FILE")
    
    summary "- ✓ Created PR: $PR_URL"
    echo "PR created: $PR_URL"
  else
    echo "gh CLI not found - branch pushed but PR not created"
    echo "Create PR manually from branch: $DEPLOY_BRANCH"
    summary "- ✓ Branch pushed: $DEPLOY_BRANCH (create PR manually)"
  fi
  
  rm "$PR_BODY_FILE"
fi

summary ""
