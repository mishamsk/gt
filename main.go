package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	version            = "0.1.0"
	defaultWorktreeDir = ".worktrees"
	configFileName     = "config.json"
	configDirName      = "gt"
)

type Config struct {
	WorktreeDir string `json:"worktree_dir,omitempty"`
	Shell       string `json:"shell,omitempty"`
	PostCreate  string `json:"post_create,omitempty"`
}

type Worktree struct {
	Path       string
	Branch     string
	Head       string
	IsDirty    bool
	LastCommit CommitInfo
	IsCurrent  bool
	Ahead      int
	Behind     int
}

type CommitInfo struct {
	Hash    string
	Message string
	Date    time.Time
	Author  string
}

type model struct {
	worktrees        []Worktree
	filtered         []Worktree
	cursor           int
	scrollOffset     int
	searchTerm       string
	width            int
	height           int
	quitting         bool
	inputMode        inputMode
	inputValue       string
	confirmDelete    bool
	deleteTarget     *Worktree
	repoPath         string
	config           *Config
	err              error
	statusMessage    string
	statusTimeout    time.Time
	defaultBranch    string
	previousWorktree string
	actionCursor     int
	actions          []action
}

type inputMode int

const (
	modeNormal inputMode = iota
	modeSearch
	modeNewBranch
	modeNewPath
	modeActions
)

type action struct {
	key   string
	label string
	fn    func(wt *Worktree, config *Config) error
}

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("220")).
			MarginBottom(1)

	searchStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("86"))

	dimStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("240"))

	selectedStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("236")).
			Bold(true)

	currentStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("82")).
			Bold(true)

	dirtyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("214"))

	branchStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("141")).
			Bold(true)

	helpStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("240"))

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196")).
			Bold(true)

	successStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("82"))

	aheadStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("82"))

	behindStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("214"))
)

func getConfigPath() string {
	configHome, err := os.UserConfigDir()
	if err != nil {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".config", configDirName, configFileName)
	}
	return filepath.Join(configHome, configDirName, configFileName)
}

func loadConfig() (*Config, error) {
	configPath := getConfigPath()
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, err
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return &Config{}, nil
	}

	return &config, nil
}

func saveConfig(config *Config) error {
	configPath := getConfigPath()
	configDir := filepath.Dir(configPath)

	if err := os.MkdirAll(configDir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(configPath, data, 0644)
}

func getCurrentRepoPath() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("not in a git repository")
	}
	return strings.TrimSpace(string(output)), nil
}

func getAheadBehind(worktreePath, branch, defaultBranch string) (ahead, behind int) {
	if branch == "" || branch == defaultBranch {
		return 0, 0
	}

	// Get count of commits ahead and behind
	cmd := exec.Command("git", "rev-list", "--left-right", "--count", fmt.Sprintf("%s...%s", defaultBranch, branch))
	cmd.Dir = worktreePath
	output, err := cmd.Output()
	if err != nil {
		return 0, 0
	}

	parts := strings.Fields(strings.TrimSpace(string(output)))
	if len(parts) == 2 {
		behind, _ = strconv.Atoi(parts[0])
		ahead, _ = strconv.Atoi(parts[1])
	}

	return ahead, behind
}

func getWorktrees(repoPath string) ([]Worktree, error) {
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	// Get default branch early for ahead/behind calculation
	defaultBranch := getDefaultBranch(repoPath)

	var worktrees []Worktree
	lines := strings.Split(string(output), "\n")

	var current Worktree
	for _, line := range lines {
		if strings.HasPrefix(line, "worktree ") {
			if current.Path != "" {
				worktrees = append(worktrees, current)
			}
			current = Worktree{
				Path: strings.TrimPrefix(line, "worktree "),
			}
		} else if strings.HasPrefix(line, "HEAD ") {
			current.Head = strings.TrimPrefix(line, "HEAD ")
		} else if strings.HasPrefix(line, "branch ") {
			current.Branch = strings.TrimPrefix(line, "branch ")
			current.Branch = strings.TrimPrefix(current.Branch, "refs/heads/")
		} else if line == "" && current.Path != "" {
			worktrees = append(worktrees, current)
			current = Worktree{}
		}
	}
	if current.Path != "" {
		worktrees = append(worktrees, current)
	}

	// Get current directory to mark current worktree
	cwd, _ := os.Getwd()

	// Get additional info for each worktree
	for i := range worktrees {
		worktrees[i].IsCurrent = strings.HasPrefix(cwd, worktrees[i].Path)

		// Check if dirty
		cmd := exec.Command("git", "status", "--porcelain")
		cmd.Dir = worktrees[i].Path
		output, err := cmd.Output()
		if err == nil {
			worktrees[i].IsDirty = len(strings.TrimSpace(string(output))) > 0
		}

		// Get last commit info
		cmd = exec.Command("git", "log", "-1", "--pretty=format:%H|%s|%ai|%an")
		cmd.Dir = worktrees[i].Path
		output, err = cmd.Output()
		if err == nil && len(output) > 0 {
			parts := strings.Split(string(output), "|")
			if len(parts) >= 4 {
				worktrees[i].LastCommit.Hash = parts[0][:7]
				worktrees[i].LastCommit.Message = parts[1]
				if t, err := time.Parse("2006-01-02 15:04:05 -0700", parts[2]); err == nil {
					worktrees[i].LastCommit.Date = t
				}
				worktrees[i].LastCommit.Author = parts[3]
			}
		}

		// Get ahead/behind count relative to default branch
		worktrees[i].Ahead, worktrees[i].Behind = getAheadBehind(worktrees[i].Path, worktrees[i].Branch, defaultBranch)
	}

	return worktrees, nil
}

func filterWorktrees(worktrees []Worktree, search string) []Worktree {
	if search == "" {
		return worktrees
	}

	search = strings.ToLower(search)
	var filtered []Worktree
	for _, wt := range worktrees {
		if strings.Contains(strings.ToLower(wt.Branch), search) ||
			strings.Contains(strings.ToLower(wt.LastCommit.Message), search) ||
			strings.Contains(strings.ToLower(wt.Path), search) {
			filtered = append(filtered, wt)
		}
	}
	return filtered
}

func formatRelativeTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	now := time.Now()
	diff := now.Sub(t)

	switch {
	case diff < time.Minute:
		return "just now"
	case diff < time.Hour:
		mins := int(diff.Minutes())
		if mins == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", mins)
	case diff < 24*time.Hour:
		hours := int(diff.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	case diff < 7*24*time.Hour:
		days := int(diff.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	default:
		return t.Format("Jan 2, 2006")
	}
}

func getActions() []action {
	return []action{
		{
			key:   "e",
			label: "Open in editor ($EDITOR)",
			fn: func(wt *Worktree, config *Config) error {
				editor := os.Getenv("EDITOR")
				if editor == "" {
					editor = "vim"
				}
				cmd := exec.Command(editor, wt.Path)
				cmd.Stdin = os.Stdin
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr
				return cmd.Run()
			},
		},
		{
			key:   "c",
			label: "Open in VS Code",
			fn: func(wt *Worktree, config *Config) error {
				cmd := exec.Command("code", wt.Path)
				return cmd.Run()
			},
		},
		{
			key:   "t",
			label: "Open terminal here",
			fn: func(wt *Worktree, config *Config) error {
				shell := getShell(config)
				cmd := exec.Command(shell)
				cmd.Dir = wt.Path
				cmd.Stdin = os.Stdin
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr
				return cmd.Run()
			},
		},
		{
			key:   "f",
			label: "Open in Finder",
			fn: func(wt *Worktree, config *Config) error {
				cmd := exec.Command("open", wt.Path)
				return cmd.Run()
			},
		},
		{
			key:   "p",
			label: "Copy path to clipboard",
			fn: func(wt *Worktree, config *Config) error {
				cmd := exec.Command("pbcopy")
				cmd.Stdin = strings.NewReader(wt.Path)
				return cmd.Run()
			},
		},
	}
}

func initialModel() model {
	repoPath, err := getCurrentRepoPath()
	if err != nil {
		return model{err: err}
	}

	config, _ := loadConfig()
	if config == nil {
		config = &Config{}
	}

	worktrees, err := getWorktrees(repoPath)
	if err != nil {
		return model{err: err}
	}

	// Get default branch for ^ shortcut
	defaultBranch := getDefaultBranch(repoPath)

	// Find current worktree path for tracking previous
	var currentPath string
	for _, wt := range worktrees {
		if wt.IsCurrent {
			currentPath = wt.Path
			break
		}
	}

	m := model{
		worktrees:        worktrees,
		filtered:         worktrees,
		repoPath:         repoPath,
		config:           config,
		defaultBranch:    defaultBranch,
		previousWorktree: currentPath,
		actions:          getActions(),
	}

	return m
}

func (m model) Init() tea.Cmd {
	return nil
}

type tickMsg time.Time

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tickMsg:
		if !m.statusTimeout.IsZero() && time.Now().After(m.statusTimeout) {
			m.statusMessage = ""
			m.statusTimeout = time.Time{}
		}
		return m, tickCmd()

	case tea.KeyMsg:
		if m.confirmDelete {
			switch msg.String() {
			case "y", "Y":
				if m.deleteTarget != nil {
					if err := deleteWorktree(m.repoPath, m.deleteTarget.Path); err != nil {
						m.err = err
					} else {
						m.statusMessage = fmt.Sprintf("Deleted worktree: %s", m.deleteTarget.Branch)
						m.statusTimeout = time.Now().Add(3 * time.Second)
						// Refresh worktrees
						if worktrees, err := getWorktrees(m.repoPath); err == nil {
							m.worktrees = worktrees
							m.filtered = filterWorktrees(worktrees, m.searchTerm)
						}
					}
				}
				m.confirmDelete = false
				m.deleteTarget = nil
				return m, tickCmd()
			case "n", "N", "esc", "ctrl+c":
				m.confirmDelete = false
				m.deleteTarget = nil
				return m, nil
			}
			return m, nil
		}

		switch m.inputMode {
		case modeSearch:
			switch msg.String() {
			case "esc", "ctrl+c":
				m.inputMode = modeNormal
				m.searchTerm = ""
				m.filtered = m.worktrees
				return m, nil
			case "enter":
				m.inputMode = modeNormal
				return m, nil
			case "backspace":
				if len(m.searchTerm) > 0 {
					m.searchTerm = m.searchTerm[:len(m.searchTerm)-1]
					m.filtered = filterWorktrees(m.worktrees, m.searchTerm)
				}
				return m, nil
			default:
				if len(msg.String()) == 1 {
					m.searchTerm += msg.String()
					m.filtered = filterWorktrees(m.worktrees, m.searchTerm)
				}
				return m, nil
			}

		case modeNewBranch:
			switch msg.String() {
			case "esc", "ctrl+c":
				m.inputMode = modeNormal
				m.inputValue = ""
				return m, nil
			case "enter":
				if m.inputValue != "" {
					if err := createWorktree(m.repoPath, m.inputValue, m.config); err != nil {
						m.err = err
					} else {
						m.statusMessage = fmt.Sprintf("Created worktree: %s", m.inputValue)
						m.statusTimeout = time.Now().Add(3 * time.Second)
						// Refresh worktrees
						if worktrees, err := getWorktrees(m.repoPath); err == nil {
							m.worktrees = worktrees
							m.filtered = filterWorktrees(worktrees, m.searchTerm)
						}
					}
				}
				m.inputMode = modeNormal
				m.inputValue = ""
				return m, tickCmd()
			case "backspace":
				if len(m.inputValue) > 0 {
					m.inputValue = m.inputValue[:len(m.inputValue)-1]
				}
				return m, nil
			default:
				if len(msg.String()) == 1 || msg.String() == "/" || msg.String() == "-" {
					m.inputValue += msg.String()
				}
				return m, nil
			}

		case modeActions:
			switch msg.String() {
			case "esc", "ctrl+c", "q":
				m.inputMode = modeNormal
				return m, nil
			case "up", "k":
				if m.actionCursor > 0 {
					m.actionCursor--
				}
				return m, nil
			case "down", "j":
				if m.actionCursor < len(m.actions)-1 {
					m.actionCursor++
				}
				return m, nil
			case "enter":
				if m.cursor < len(m.filtered) && m.actionCursor < len(m.actions) {
					wt := m.filtered[m.cursor]
					action := m.actions[m.actionCursor]
					m.inputMode = modeNormal
					m.quitting = true
					fmt.Printf("\n\033[2mRunning: %s\033[0m\n", action.label)
					if err := action.fn(&wt, m.config); err != nil {
						fmt.Fprintf(os.Stderr, "Action failed: %v\n", err)
					}
					return m, tea.Quit
				}
				return m, nil
			default:
				// Check for action shortcut keys
				for _, action := range m.actions {
					if msg.String() == action.key {
						if m.cursor < len(m.filtered) {
							wt := m.filtered[m.cursor]
							m.inputMode = modeNormal
							m.quitting = true
							fmt.Printf("\n\033[2mRunning: %s\033[0m\n", action.label)
							if err := action.fn(&wt, m.config); err != nil {
								fmt.Fprintf(os.Stderr, "Action failed: %v\n", err)
							}
							return m, tea.Quit
						}
					}
				}
				return m, nil
			}

		default: // modeNormal
			switch msg.String() {
			case "q", "ctrl+c":
				m.quitting = true
				return m, tea.Quit

			case "up", "k":
				if m.cursor > 0 {
					m.cursor--
				}
				return m, nil

			case "down", "j":
				if m.cursor < len(m.filtered)-1 {
					m.cursor++
				}
				return m, nil

			case "/":
				m.inputMode = modeSearch
				m.searchTerm = ""
				return m, nil

			case "n":
				m.inputMode = modeNewBranch
				m.inputValue = ""
				return m, nil

			case "d":
				if m.cursor < len(m.filtered) {
					wt := &m.filtered[m.cursor]
					if strings.HasSuffix(m.repoPath, wt.Path) {
						m.err = fmt.Errorf("cannot delete main worktree")
						return m, nil
					}
					m.confirmDelete = true
					m.deleteTarget = wt
				}
				return m, nil

			case "r":
				// Refresh worktrees
				if worktrees, err := getWorktrees(m.repoPath); err == nil {
					m.worktrees = worktrees
					m.filtered = filterWorktrees(worktrees, m.searchTerm)
					m.statusMessage = "Refreshed"
					m.statusTimeout = time.Now().Add(2 * time.Second)
				}
				return m, tickCmd()

			case "^":
				// Jump to default branch worktree
				for i, wt := range m.filtered {
					if wt.Branch == m.defaultBranch {
						m.cursor = i
						m.statusMessage = fmt.Sprintf("Jumped to default branch: %s", m.defaultBranch)
						m.statusTimeout = time.Now().Add(2 * time.Second)
						break
					}
				}
				return m, tickCmd()

			case "-":
				// Jump to previous worktree
				if m.previousWorktree != "" {
					for i, wt := range m.filtered {
						if wt.Path == m.previousWorktree {
							m.cursor = i
							m.statusMessage = "Jumped to previous worktree"
							m.statusTimeout = time.Now().Add(2 * time.Second)
							break
						}
					}
				}
				return m, tickCmd()

			case "@":
				// Jump to current worktree
				for i, wt := range m.filtered {
					if wt.IsCurrent {
						m.cursor = i
						m.statusMessage = "Jumped to current worktree"
						m.statusTimeout = time.Now().Add(2 * time.Second)
						break
					}
				}
				return m, tickCmd()

			case "a":
				// Open actions menu
				if m.cursor < len(m.filtered) {
					m.inputMode = modeActions
					m.actionCursor = 0
				}
				return m, nil

			case "enter":
				if m.cursor < len(m.filtered) {
					wt := m.filtered[m.cursor]

					// Track previous worktree before switching
					for _, w := range m.worktrees {
						if w.IsCurrent {
							m.previousWorktree = w.Path
							break
						}
					}

					// Exit the TUI and switch to the worktree
					m.quitting = true
					fmt.Printf("\n\033[2mSwitching to %s...\033[0m\n", wt.Path)
					
					// Change to the worktree directory
					if err := os.Chdir(wt.Path); err != nil {
						m.err = err
						return m, nil
					}
					
					// Start a new shell in the worktree directory
					shell := getShell(m.config)
					cmd := exec.Command(shell)
					cmd.Stdin = os.Stdin
					cmd.Stdout = os.Stdout
					cmd.Stderr = os.Stderr
					cmd.Dir = wt.Path
					
					// Execute the shell after quitting the TUI
					return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
						if err != nil {
							return err
						}
						return tea.Quit()
					})
				}
				return m, nil
			}
		}
	}

	return m, nil
}

func getShell(config *Config) string {
	if config != nil && config.Shell != "" {
		return config.Shell
	}
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell
	}
	return "/bin/bash"
}

func ensureGitignoreEntry(repoPath, entry string) error {
	gitignorePath := filepath.Join(repoPath, ".gitignore")
	
	// Ensure entry ends with / if it's a directory
	if !strings.HasSuffix(entry, "/") {
		entry = entry + "/"
	}
	
	// Read existing .gitignore content
	content, err := os.ReadFile(gitignorePath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	
	// Check if entry already exists
	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == entry || trimmed == strings.TrimRight(entry, "/") {
			// Entry already exists
			return nil
		}
	}
	
	// Add entry to .gitignore
	newContent := string(content)
	if len(content) > 0 && !strings.HasSuffix(newContent, "\n") {
		newContent += "\n"
	}
	
	// Add a comment if this is the first worktree entry
	hasWorktreeComment := false
	for _, line := range lines {
		if strings.Contains(line, "Git worktrees") {
			hasWorktreeComment = true
			break
		}
	}
	
	if !hasWorktreeComment && len(content) > 0 {
		newContent += "\n# Git worktrees\n"
	} else if len(content) == 0 {
		newContent = "# Git worktrees\n"
	}
	
	newContent += entry + "\n"
	
	return os.WriteFile(gitignorePath, []byte(newContent), 0644)
}

func createWorktreeFromBranch(repoPath, worktreeName, sourceBranch string, config *Config) error {
	// Determine worktree directory
	worktreeDir := defaultWorktreeDir
	if config != nil && config.WorktreeDir != "" {
		worktreeDir = config.WorktreeDir
	}

	// Handle absolute vs relative paths
	if !filepath.IsAbs(worktreeDir) {
		worktreeDir = filepath.Join(repoPath, worktreeDir)
	}

	// Create worktree directory if it doesn't exist
	if err := os.MkdirAll(worktreeDir, 0755); err != nil {
		return err
	}

	// Add worktree directory to .gitignore if it's within the repo
	if strings.HasPrefix(worktreeDir, repoPath) {
		relPath, _ := filepath.Rel(repoPath, worktreeDir)
		if relPath != "" && !strings.HasPrefix(relPath, "..") {
			if err := ensureGitignoreEntry(repoPath, relPath); err != nil {
				// Don't fail the worktree creation if we can't update .gitignore
				// Just continue with a warning
				fmt.Fprintf(os.Stderr, "Warning: Could not update .gitignore: %v\n", err)
			}
		}
	}

	// Generate worktree path
	worktreePath := filepath.Join(worktreeDir, strings.ReplaceAll(worktreeName, "/", "-"))

	// Create worktree with new branch from source branch
	cmd := exec.Command("git", "worktree", "add", "-b", worktreeName, worktreePath, sourceBranch)
	cmd.Dir = repoPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		// If branch already exists, try without -b flag
		cmd = exec.Command("git", "worktree", "add", worktreePath, worktreeName)
		cmd.Dir = repoPath
		output, err = cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to create worktree: %s", string(output))
		}
	}

	return nil
}

func createWorktree(repoPath, branch string, config *Config) error {
	// Determine worktree directory
	worktreeDir := defaultWorktreeDir
	if config != nil && config.WorktreeDir != "" {
		worktreeDir = config.WorktreeDir
	}

	// Handle absolute vs relative paths
	if !filepath.IsAbs(worktreeDir) {
		worktreeDir = filepath.Join(repoPath, worktreeDir)
	}

	// Create worktree directory if it doesn't exist
	if err := os.MkdirAll(worktreeDir, 0755); err != nil {
		return err
	}

	// Add worktree directory to .gitignore if it's within the repo
	if strings.HasPrefix(worktreeDir, repoPath) {
		relPath, _ := filepath.Rel(repoPath, worktreeDir)
		if relPath != "" && !strings.HasPrefix(relPath, "..") {
			if err := ensureGitignoreEntry(repoPath, relPath); err != nil {
				// Don't fail the worktree creation if we can't update .gitignore
				// Just continue with a warning
				fmt.Fprintf(os.Stderr, "Warning: Could not update .gitignore: %v\n", err)
			}
		}
	}

	// Generate worktree path
	worktreePath := filepath.Join(worktreeDir, strings.ReplaceAll(branch, "/", "-"))

	// Check if branch exists locally or remotely
	cmd := exec.Command("git", "rev-parse", "--verify", branch)
	cmd.Dir = repoPath
	if err := cmd.Run(); err != nil {
		// Try as remote branch
		cmd = exec.Command("git", "ls-remote", "--heads", "origin", branch)
		cmd.Dir = repoPath
		output, err := cmd.Output()
		if err != nil || len(output) == 0 {
			// Create new branch
			cmd = exec.Command("git", "worktree", "add", "-b", branch, worktreePath)
		} else {
			// Checkout existing remote branch
			cmd = exec.Command("git", "worktree", "add", worktreePath, branch)
		}
	} else {
		// Checkout existing local branch
		cmd = exec.Command("git", "worktree", "add", worktreePath, branch)
	}

	cmd.Dir = repoPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to create worktree: %s", string(output))
	}

	return nil
}

func deleteWorktree(repoPath, worktreePath string) error {
	cmd := exec.Command("git", "worktree", "remove", worktreePath, "--force")
	cmd.Dir = repoPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to remove worktree: %s", string(output))
	}
	return nil
}

func (m model) View() string {
	if m.err != nil {
		return errorStyle.Render(fmt.Sprintf("Error: %v\n\nPress q to quit.", m.err))
	}

	var s strings.Builder

	// Title
	title := fmt.Sprintf("Git Worktrees - %s", m.repoPath)
	s.WriteString(titleStyle.Render(title) + "\n")

	// Search or input
	switch m.inputMode {
	case modeSearch:
		s.WriteString(searchStyle.Render("Search: ") + m.searchTerm + "█\n\n")
	case modeNewBranch:
		s.WriteString(searchStyle.Render("New branch name: ") + m.inputValue + "█\n\n")
	case modeActions:
		if m.cursor < len(m.filtered) {
			wt := m.filtered[m.cursor]
			s.WriteString(searchStyle.Render(fmt.Sprintf("Actions for %s:\n", wt.Branch)))
			for i, action := range m.actions {
				cursor := "  "
				if i == m.actionCursor {
					cursor = "▸ "
				}
				line := fmt.Sprintf("%s[%s] %s", cursor, action.key, action.label)
				if i == m.actionCursor {
					s.WriteString(selectedStyle.Render(line) + "\n")
				} else {
					s.WriteString(line + "\n")
				}
			}
			s.WriteString(helpStyle.Render("\n[enter] select  [esc] cancel\n"))
		}
		return s.String()
	default:
		if m.searchTerm != "" {
			s.WriteString(searchStyle.Render("Search: ") + dimStyle.Render(m.searchTerm) + "\n\n")
		} else {
			s.WriteString("\n")
		}
	}

	// Confirmation dialog
	if m.confirmDelete && m.deleteTarget != nil {
		s.WriteString(errorStyle.Render(fmt.Sprintf("\nDelete worktree '%s'? [y/N] ", m.deleteTarget.Branch)))
		return s.String()
	}

	// Worktree list
	if len(m.filtered) == 0 {
		s.WriteString(dimStyle.Render("  No worktrees found\n"))
	} else {
		viewportHeight := m.height - 8 // Account for header and footer
		if viewportHeight < 5 {
			viewportHeight = 5
		}

		// Adjust scroll offset
		if m.cursor >= m.scrollOffset+viewportHeight {
			m.scrollOffset = m.cursor - viewportHeight + 1
		} else if m.cursor < m.scrollOffset {
			m.scrollOffset = m.cursor
		}

		for i := m.scrollOffset; i < len(m.filtered) && i < m.scrollOffset+viewportHeight; i++ {
			wt := m.filtered[i]
			
			// Cursor indicator
			cursor := "  "
			if i == m.cursor {
				cursor = "▸ "
			}

			// Branch name
			branch := wt.Branch
			if branch == "" {
				branch = "(detached)"
			}
			if wt.IsCurrent {
				branch = currentStyle.Render("● " + branch)
			} else {
				branch = branchStyle.Render(branch)
			}

			// Status indicator
			status := "✓"
			if wt.IsDirty {
				status = dirtyStyle.Render("●")
			}

			// Ahead/behind indicator
			var aheadBehind string
			if wt.Ahead > 0 || wt.Behind > 0 {
				var parts []string
				if wt.Ahead > 0 {
					parts = append(parts, aheadStyle.Render(fmt.Sprintf("↑%d", wt.Ahead)))
				}
				if wt.Behind > 0 {
					parts = append(parts, behindStyle.Render(fmt.Sprintf("↓%d", wt.Behind)))
				}
				aheadBehind = " " + strings.Join(parts, " ")
			}

			// Commit info
			commitMsg := wt.LastCommit.Message
			if len(commitMsg) > 40 {
				commitMsg = commitMsg[:40] + "..."
			}

			relTime := formatRelativeTime(wt.LastCommit.Date)

			// Format line
			line := fmt.Sprintf("%s%-20s %s%s  %s",
				cursor,
				branch,
				status,
				aheadBehind,
				dimStyle.Render(fmt.Sprintf("%s (%s)", commitMsg, relTime)),
			)

			if i == m.cursor {
				s.WriteString(selectedStyle.Render(line) + "\n")
			} else {
				s.WriteString(line + "\n")
			}
		}
	}

	// Status message
	if m.statusMessage != "" {
		s.WriteString("\n" + successStyle.Render(m.statusMessage) + "\n")
	}

	// Help
	s.WriteString("\n")
	if m.inputMode == modeNormal {
		help := "[n]ew  [d]elete  [a]ctions  [enter] switch  [/] search  [r]efresh  [^] default  [-] prev  [@] current  [q]uit"
		s.WriteString(helpStyle.Render(help))
	} else {
		help := "[enter] confirm  [esc] cancel"
		s.WriteString(helpStyle.Render(help))
	}

	return s.String()
}

func generateBashCompletion() string {
	return `_gt_completions() {
    local cur="${COMP_WORDS[COMP_CWORD]}"
    local prev="${COMP_WORDS[COMP_CWORD-1]}"

    # Get list of branches for completion
    if [[ "$prev" == "gt" ]] || [[ "$prev" == "-x" ]] || [[ "$prev" == "--execute" ]]; then
        local branches=$(git branch --format='%(refname:short)' 2>/dev/null)
        local worktrees=$(git worktree list --porcelain 2>/dev/null | grep "^branch" | sed 's/branch refs\/heads\///')
        COMPREPLY=($(compgen -W "$branches $worktrees -h --help -v --version -x --execute" -- "$cur"))
    elif [[ "$cur" == -* ]]; then
        COMPREPLY=($(compgen -W "-h --help -v --version -x --execute" -- "$cur"))
    else
        local branches=$(git branch --format='%(refname:short)' 2>/dev/null)
        COMPREPLY=($(compgen -W "$branches" -- "$cur"))
    fi
}

complete -F _gt_completions gt
`
}

func generateZshCompletion() string {
	return `#compdef gt

_gt() {
    local -a branches worktrees

    # Get branches
    branches=(${(f)"$(git branch --format='%(refname:short)' 2>/dev/null)"})

    # Get worktree branches
    worktrees=(${(f)"$(git worktree list --porcelain 2>/dev/null | grep '^branch' | sed 's/branch refs\/heads\///')"})

    _arguments \
        '(-h --help)'{-h,--help}'[Show help message]' \
        '(-v --version)'{-v,--version}'[Show version]' \
        '(-x --execute)'{-x,--execute}'[Execute command after switch]:command:_command_names' \
        '1:worktree name:->worktree' \
        '2:source branch:->branch'

    case $state in
        worktree|branch)
            _describe -t branches 'branches' branches
            _describe -t worktrees 'worktrees' worktrees
            ;;
    esac
}

_gt "$@"
`
}

func generateFishCompletion() string {
	return `# Fish completion for gt

function __gt_branches
    git branch --format='%(refname:short)' 2>/dev/null
end

function __gt_worktrees
    git worktree list --porcelain 2>/dev/null | string match -r '^branch refs/heads/(.*)' | string replace 'branch refs/heads/' ''
end

# Disable file completion
complete -c gt -f

# Options
complete -c gt -s h -l help -d 'Show help message'
complete -c gt -s v -l version -d 'Show version'
complete -c gt -s x -l execute -d 'Execute command after switch' -r

# Subcommands
complete -c gt -n '__fish_is_first_arg' -a '(__gt_branches)' -d 'Branch'
complete -c gt -n '__fish_is_first_arg' -a '(__gt_worktrees)' -d 'Worktree'
complete -c gt -n 'not __fish_is_first_arg' -a '(__gt_branches)' -d 'Source branch'
`
}

func printCompletion(shell string) {
	switch shell {
	case "bash":
		fmt.Print(generateBashCompletion())
	case "zsh":
		fmt.Print(generateZshCompletion())
	case "fish":
		fmt.Print(generateFishCompletion())
	default:
		fmt.Fprintf(os.Stderr, "Unknown shell: %s\nSupported shells: bash, zsh, fish\n", shell)
		os.Exit(1)
	}
}

func printHelp() {
	help := fmt.Sprintf(`gt - Git Worktree Manager v%s

USAGE:
    gt                           Open interactive worktree manager
    gt <name>                    Create worktree from current branch and switch to it
    gt <name> <branch>           Create worktree from specified branch and switch to it
    gt <name> -x <cmd>           Create worktree and execute command instead of shell
    gt completion <shell>        Generate shell completion script (bash, zsh, fish)
    gt -h, --help                Show this help message
    gt -v, --version             Show version information

OPTIONS:
    -x, --execute <cmd>          Execute command after switching (instead of shell)

EXAMPLES:
    gt                           # Open TUI to manage worktrees
    gt feature-xyz               # Create worktree 'feature-xyz' from current branch
    gt fix-bug main              # Create worktree 'fix-bug' from 'main' branch
    gt feature-xyz -x "code ."   # Create worktree and open in VS Code
    gt feature-xyz -x claude     # Create worktree and start Claude Code

SHELL COMPLETION:
    # Bash: Add to ~/.bashrc
    eval "$(gt completion bash)"

    # Zsh: Add to ~/.zshrc
    eval "$(gt completion zsh)"

    # Fish: Add to ~/.config/fish/config.fish
    gt completion fish | source

INTERACTIVE MODE COMMANDS:
    n        Create new worktree
    d        Delete selected worktree
    a        Open actions menu for selected worktree
    enter    Switch to selected worktree
    /        Search worktrees
    r        Refresh list
    ^        Jump to default branch worktree
    -        Jump to previous worktree
    @        Jump to current worktree
    q        Quit

QUICK ACTIONS (from actions menu):
    e        Open in editor ($EDITOR)
    c        Open in VS Code
    t        Open terminal here
    f        Open in Finder
    p        Copy path to clipboard

CONFIGURATION:
    Config file: ~/.config/gt/config.json

    Options:
    - worktree_dir: Directory for worktrees (default: .worktrees)
                    Use absolute path to place worktrees outside repo
                    Use "../" prefix to place worktrees as siblings
    - shell: Shell to use when switching (default: $SHELL or /bin/bash)
    - post_create: Command to run after creating a worktree (e.g., "npm install")

    Example configs:

    # Default (inside repo):
    { "worktree_dir": ".worktrees" }

    # Sibling directories (like worktrunk):
    { "worktree_dir": "../gt-worktrees" }

    # Absolute path:
    { "worktree_dir": "/Users/me/worktrees/gt" }

    # With post-create hook:
    {
      "worktree_dir": "../gt-worktrees",
      "post_create": "npm install"
    }
`, version)
	fmt.Print(help)
}

func getCurrentBranch(repoPath string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func getDefaultBranch(repoPath string) string {
	// Try to get the default branch from remote
	cmd := exec.Command("git", "symbolic-ref", "refs/remotes/origin/HEAD")
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err == nil {
		ref := strings.TrimSpace(string(output))
		// refs/remotes/origin/main -> main
		parts := strings.Split(ref, "/")
		if len(parts) > 0 {
			return parts[len(parts)-1]
		}
	}

	// Fallback: check common default branch names
	for _, branch := range []string{"main", "master"} {
		cmd = exec.Command("git", "rev-parse", "--verify", branch)
		cmd.Dir = repoPath
		if err := cmd.Run(); err == nil {
			return branch
		}
	}

	return "main"
}

func runPostCreateHook(worktreePath string, config *Config) error {
	if config == nil || config.PostCreate == "" {
		return nil
	}

	fmt.Printf("\033[2mRunning post-create hook: %s\033[0m\n", config.PostCreate)

	shell := getShell(config)
	cmd := exec.Command(shell, "-c", config.PostCreate)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Dir = worktreePath

	return cmd.Run()
}

func switchToWorktree(worktreePath string, config *Config, executeCmd string) error {
	// Change to the worktree directory
	if err := os.Chdir(worktreePath); err != nil {
		return err
	}

	fmt.Printf("\033[2mSwitched to %s\033[0m\n", worktreePath)

	// If an execute command is specified, run it instead of starting a shell
	if executeCmd != "" {
		shell := getShell(config)
		cmd := exec.Command(shell, "-c", executeCmd)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Dir = worktreePath
		return cmd.Run()
	}

	// Start a new shell in the worktree directory
	shell := getShell(config)
	cmd := exec.Command(shell)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Dir = worktreePath

	return cmd.Run()
}

func parseArgs() (worktreeName, sourceBranch, executeCmd, completionShell string, showHelp, showVersion bool) {
	args := os.Args[1:]
	var positional []string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help" || arg == "help":
			showHelp = true
			return
		case arg == "-v" || arg == "--version" || arg == "version":
			showVersion = true
			return
		case arg == "completion":
			if i+1 < len(args) {
				i++
				completionShell = args[i]
			} else {
				completionShell = "bash"
			}
			return
		case arg == "-x" || arg == "--execute":
			if i+1 < len(args) {
				i++
				executeCmd = args[i]
			}
		case strings.HasPrefix(arg, "-x="):
			executeCmd = strings.TrimPrefix(arg, "-x=")
		case strings.HasPrefix(arg, "--execute="):
			executeCmd = strings.TrimPrefix(arg, "--execute=")
		case !strings.HasPrefix(arg, "-"):
			positional = append(positional, arg)
		}
	}

	if len(positional) > 0 {
		worktreeName = positional[0]
	}
	if len(positional) > 1 {
		sourceBranch = positional[1]
	}

	return
}

func main() {
	worktreeName, sourceBranch, executeCmd, completionShell, showHelp, showVersion := parseArgs()

	if showHelp {
		printHelp()
		os.Exit(0)
	}

	if showVersion {
		fmt.Printf("gt version %s\n", version)
		os.Exit(0)
	}

	if completionShell != "" {
		printCompletion(completionShell)
		os.Exit(0)
	}

	// Handle worktree creation if name provided
	if worktreeName != "" {
		// Get repo path
		repoPath, err := getCurrentRepoPath()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		// Load config
		config, _ := loadConfig()
		if config == nil {
			config = &Config{}
		}

		// Determine source branch
		if sourceBranch == "" {
			// Use current branch
			sourceBranch, err = getCurrentBranch(repoPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error getting current branch: %v\n", err)
				os.Exit(1)
			}
		}

		// Create the worktree
		fmt.Printf("Creating worktree '%s' from branch '%s'...\n", worktreeName, sourceBranch)

		// First check if we need to create from an existing branch or create new
		if sourceBranch != worktreeName {
			// Create worktree from existing branch
			err = createWorktreeFromBranch(repoPath, worktreeName, sourceBranch, config)
		} else {
			// Create new branch with worktree
			err = createWorktree(repoPath, worktreeName, config)
		}

		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating worktree: %v\n", err)
			os.Exit(1)
		}

		// Determine worktree path
		worktreeDir := defaultWorktreeDir
		if config != nil && config.WorktreeDir != "" {
			worktreeDir = config.WorktreeDir
		}
		if !filepath.IsAbs(worktreeDir) {
			worktreeDir = filepath.Join(repoPath, worktreeDir)
		}
		worktreePath := filepath.Join(worktreeDir, strings.ReplaceAll(worktreeName, "/", "-"))

		// Run post-create hook if configured
		if err := runPostCreateHook(worktreePath, config); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: post-create hook failed: %v\n", err)
		}

		// Switch to the new worktree
		if err := switchToWorktree(worktreePath, config, executeCmd); err != nil {
			fmt.Fprintf(os.Stderr, "Error switching to worktree: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	
	// No arguments - run interactive mode
	p := tea.NewProgram(initialModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}