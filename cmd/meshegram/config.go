package main

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type config struct {
	botToken            string
	chatID              int64
	proxyURL            string
	allowedIDs          map[int64]struct{}
	allowedUsernames    map[string]struct{}
	nodeName            string
	nodeAddress         string
	defaultChannel      uint32
	hopLimit            uint32
	reconnectInterval   time.Duration
	prependAuthor       bool
	onlyChannels        map[string]struct{}
	ignoreChannels      map[string]struct{}
	onlyMessageRegexp   *regexp.Regexp
	ignoreMessageRegexp *regexp.Regexp
}

func (c *config) allowedCount() int {
	return len(c.allowedIDs) + len(c.allowedUsernames)
}

func loadConfig() (*config, error) {
	cfg := &config{
		botToken:      os.Getenv("MESHEGRAM_TG_TOKEN"),
		proxyURL:      os.Getenv("MESHEGRAM_TG_PROXY"),
		nodeName:      os.Getenv("MESHEGRAM_NODE_NAME"),
		nodeAddress:   os.Getenv("MESHEGRAM_NODE"),
		prependAuthor: true,
	}

	if cfg.botToken == "" {
		return nil, fmt.Errorf("MESHEGRAM_TG_TOKEN is required")
	}
	if cfg.nodeAddress == "" {
		return nil, fmt.Errorf("MESHEGRAM_NODE is required (host or host:port)")
	}
	if cfg.nodeName == "" {
		cfg.nodeName = cfg.nodeAddress
	}

	chatStr := os.Getenv("MESHEGRAM_TG_CHAT")
	if chatStr == "" {
		return nil, fmt.Errorf("MESHEGRAM_TG_CHAT is required")
	}
	id, err := strconv.ParseInt(chatStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("MESHEGRAM_TG_CHAT must be integer (got %q): %w", chatStr, err)
	}
	cfg.chatID = id

	usersStr := os.Getenv("MESHEGRAM_ALLOWED_USERS")
	if usersStr == "" {
		return nil, fmt.Errorf("MESHEGRAM_ALLOWED_USERS is required (comma-separated Telegram user IDs and/or @usernames)")
	}
	cfg.allowedIDs = make(map[int64]struct{})
	cfg.allowedUsernames = make(map[string]struct{})
	for _, raw := range strings.Split(usersStr, ",") {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		if uid, err := strconv.ParseInt(s, 10, 64); err == nil {
			cfg.allowedIDs[uid] = struct{}{}
			continue
		}
		username := strings.ToLower(strings.TrimPrefix(s, "@"))
		if username == "" {
			return nil, fmt.Errorf("MESHEGRAM_ALLOWED_USERS: empty username in entry %q", raw)
		}
		cfg.allowedUsernames[username] = struct{}{}
	}
	if cfg.allowedCount() == 0 {
		return nil, fmt.Errorf("MESHEGRAM_ALLOWED_USERS must have at least one user ID or @username")
	}

	cfg.defaultChannel = 0
	if s := os.Getenv("MESHEGRAM_CHANNEL"); s != "" {
		v, err := strconv.ParseUint(s, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("MESHEGRAM_CHANNEL %q: %w", s, err)
		}
		cfg.defaultChannel = uint32(v)
	}

	cfg.hopLimit = 3
	if s := os.Getenv("MESHEGRAM_HOP_LIMIT"); s != "" {
		v, err := strconv.ParseUint(s, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("MESHEGRAM_HOP_LIMIT %q: %w", s, err)
		}
		cfg.hopLimit = uint32(v)
	}

	cfg.reconnectInterval = 10 * time.Second
	if s := os.Getenv("MESHEGRAM_RECONNECT_INTERVAL"); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil {
			return nil, fmt.Errorf("MESHEGRAM_RECONNECT_INTERVAL %q: %w", s, err)
		}
		cfg.reconnectInterval = d
	}

	if s := os.Getenv("MESHEGRAM_PREPEND_AUTHOR"); s != "" {
		v, err := strconv.ParseBool(s)
		if err != nil {
			return nil, fmt.Errorf("MESHEGRAM_PREPEND_AUTHOR %q: %w", s, err)
		}
		cfg.prependAuthor = v
	}

	cfg.onlyChannels = parseChannelSet("MESHEGRAM_ONLY_CHANNEL")
	cfg.ignoreChannels = parseChannelSet("MESHEGRAM_IGNORE_CHANNEL")
	if cfg.onlyMessageRegexp, err = parseMessageRegexp("MESHEGRAM_ONLY_MESSAGE_REGEXP"); err != nil {
		return nil, err
	}
	if cfg.ignoreMessageRegexp, err = parseMessageRegexp("MESHEGRAM_IGNORE_MESSAGE_REGEXP"); err != nil {
		return nil, err
	}

	return cfg, nil
}

// parseChannelSet reads a comma-separated list of channel names from env and
// returns a set keyed by lowercased name (leading "#" stripped). Returns nil
// when the variable is unset or empty — the caller treats nil as "no filter".
func parseChannelSet(env string) map[string]struct{} {
	raw := os.Getenv(env)
	if raw == "" {
		return nil
	}
	out := make(map[string]struct{})
	for _, s := range strings.Split(raw, ",") {
		name := strings.TrimSpace(s)
		name = strings.TrimPrefix(name, "#")
		if name == "" {
			continue
		}
		out[strings.ToLower(name)] = struct{}{}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func parseMessageRegexp(env string) (*regexp.Regexp, error) {
	raw := os.Getenv(env)
	if raw == "" {
		return nil, nil
	}
	re, err := regexp.Compile(raw)
	if err != nil {
		return nil, fmt.Errorf("%s %q: %w", env, raw, err)
	}
	return re, nil
}

// channelAllowed applies the ONLY/IGNORE channel filters. Name matching is
// case-insensitive and "#" prefixes are ignored.
func (c *config) channelAllowed(name string) bool {
	lower := strings.ToLower(strings.TrimPrefix(name, "#"))
	if len(c.onlyChannels) > 0 {
		if _, ok := c.onlyChannels[lower]; !ok {
			return false
		}
	}
	if _, ok := c.ignoreChannels[lower]; ok {
		return false
	}
	return true
}

// messageAllowed applies the ONLY/IGNORE regexp filters to a message text.
func (c *config) messageAllowed(text string) bool {
	if c.onlyMessageRegexp != nil && !c.onlyMessageRegexp.MatchString(text) {
		return false
	}
	if c.ignoreMessageRegexp != nil && c.ignoreMessageRegexp.MatchString(text) {
		return false
	}
	return true
}
