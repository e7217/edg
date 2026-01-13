# EDG Project Guidelines

## Git and GitHub Workflow

### Language Requirements
- **All git/GitHub work must use English**
  - Issues: Write in English
  - Commits: Write commit messages in English
  - Pull Requests: Write PR titles and descriptions in English

### Branch Management
- **Never work directly on `main` branch**
  - No feature development on `main`
  - No bug fixes on `main`
  - No experimental changes on `main`

### Proper Workflow
1. Create a feature branch from `main`
2. Do all work on the feature branch
3. Create PR to merge back to `main`
4. Delete feature branch after merge

### Branch Naming Convention
- Feature: `feat/description`
- Bug fix: `fix/description`
- Hotfix: `fix/description`
- Documentation: `feat/description`

## Commit Message Format
```
<type>: <short description>

<detailed description if needed>

close <number of issue if exist>
```

**Types**: feat, fix, docs, style, refactor, test, chore
