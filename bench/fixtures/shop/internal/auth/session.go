package auth

import "time"

type Session struct {
	User    string
	Expires time.Time
}

// TODO: rotate keys every 24h
func NewSession(user string, ttl time.Duration) *Session {
	return &Session{User: user, Expires: time.Now().Add(ttl)}
}

func (s *Session) Expired(now time.Time) bool {
	return now.After(s.Expires)
}
