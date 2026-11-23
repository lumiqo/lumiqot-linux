package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const personalGitHubAccount = "personal"

var githubLoginPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$`)

type githubCLIAccount struct {
	Login       string `json:"login"`
	AccountType string `json:"accountType"`
}

func validGitHubAccountType(accountType string) bool {
	switch accountType {
	case personalGitHubAccount, "work", "ai":
		return true
	}
	return false
}

func githubCLIStatus(ctx context.Context, home string, change *githubCLIAccount) (*githubCLIAccount, error) {
	// Query only the account login. The authentication token stays in gh.
	output, err := statusCommand(ctx, 8, "gh", "api", "--hostname", "github.com", "user", "--jq", ".login")
	if err != nil {
		return nil, err
	}
	login := strings.TrimSpace(string(output))
	if !githubLoginPattern.MatchString(login) {
		return nil, errors.New("GitHub CLI did not return a valid login")
	}
	if change != nil && !strings.EqualFold(login, change.Login) {
		return nil, errors.New("the active GitHub CLI account changed; refresh Settings and try again")
	}

	settingsPath := filepath.Join(home, ".config", "lumiqot", "github-cli.json")
	preferences := map[string]string{}
	data, err := os.ReadFile(settingsPath)
	if err == nil {
		if err := json.Unmarshal(data, &preferences); err != nil || preferences == nil {
			return nil, errors.New("invalid GitHub CLI preferences in github-cli.json")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	key := strings.ToLower(login)
	accountType := preferences[key]
	if !validGitHubAccountType(accountType) {
		accountType = personalGitHubAccount
	}
	if change != nil && preferences[key] != change.AccountType {
		accountType = change.AccountType
		preferences[key] = accountType
		dir := filepath.Dir(settingsPath)
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
		if err := writeStatusJSON(dir, settingsPath, preferences); err != nil {
			return nil, fmt.Errorf("save GitHub CLI preferences: %w", err)
		}
	}
	return &githubCLIAccount{Login: login, AccountType: accountType}, nil
}
