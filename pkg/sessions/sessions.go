package sessions

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// Session represents a stored sandbox session.
type Session struct {
	URL string `json:"url"`
}

// Store manages local session storage.
type Store struct {
	path     string
	sessions map[string]Session
}

// NewStore creates a new session store.
func NewStore() (*Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	dir := filepath.Join(home, ".bridge")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create config directory: %w", err)
	}

	path := filepath.Join(dir, "sessions.json")
	s := &Store{
		path:     path,
		sessions: make(map[string]Session),
	}

	if err := s.load(); err != nil {
		if os.IsNotExist(err) {
			if err := s.save(); err != nil {
				return nil, fmt.Errorf("failed to create sessions file: %w", err)
			}
		} else {
			return nil, err
		}
	}

	return s, nil
}

func (s *Store) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}

	return json.Unmarshal(data, &s.sessions)
}

func (s *Store) save() error {
	data, err := json.MarshalIndent(s.sessions, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal sessions: %w", err)
	}

	return os.WriteFile(s.path, data, 0644)
}

// Add adds a session with the given name. Returns an error if the name already exists.
func (s *Store) Add(name string, session Session) error {
	if _, exists := s.sessions[name]; exists {
		return fmt.Errorf("session %q already exists", name)
	}

	s.sessions[name] = session
	return s.save()
}

// Get retrieves a session by name.
func (s *Store) Get(name string) (Session, bool) {
	session, ok := s.sessions[name]
	return session, ok
}

// Remove removes a session by name.
func (s *Store) Remove(name string) error {
	delete(s.sessions, name)
	return s.save()
}

// Exists checks if a session with the given name exists.
func (s *Store) Exists(name string) bool {
	_, ok := s.sessions[name]
	return ok
}

// List returns all session names.
func (s *Store) List() []string {
	names := make([]string, 0, len(s.sessions))
	for name := range s.sessions {
		names = append(names, name)
	}
	return names
}

// rfc1123Regex validates RFC 1123 compliant names.
// Must be lowercase alphanumeric, may contain hyphens, must start and end with alphanumeric.
var rfc1123Regex = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// ValidateName checks if a name is RFC 1123 compliant.
func ValidateName(name string) error {
	if len(name) == 0 {
		return fmt.Errorf("name cannot be empty")
	}
	if len(name) > 63 {
		return fmt.Errorf("name cannot exceed 63 characters")
	}
	if !rfc1123Regex.MatchString(name) {
		return fmt.Errorf("name must be RFC 1123 compliant: lowercase alphanumeric, may contain hyphens, must start and end with alphanumeric")
	}
	return nil
}

// adjectives for name generation
var adjectives = []string{
	"quick", "bright", "calm", "bold", "cool", "keen", "swift", "warm",
	"fresh", "clear", "neat", "fair", "glad", "kind", "wise", "brave",
}

// nouns for name generation
var nouns = []string{
	"fox", "owl", "bear", "wolf", "hawk", "deer", "lynx", "seal",
	"hare", "crow", "dove", "frog", "moth", "newt", "wren", "swan",
}

// GenerateName generates a unique RFC 1123 compliant name.
func (s *Store) GenerateName() string {
	for {
		adj := adjectives[randomInt(len(adjectives))]
		noun := nouns[randomInt(len(nouns))]
		suffix := randomSuffix()
		name := fmt.Sprintf("%s-%s-%s", adj, noun, suffix)

		if !s.Exists(name) {
			return name
		}
	}
}

func randomInt(max int) int {
	b := make([]byte, 1)
	rand.Read(b)
	return int(b[0]) % max
}

func randomSuffix() string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 4)
	rand.Read(b)
	for i := range b {
		b[i] = chars[int(b[i])%len(chars)]
	}
	return string(b)
}
