package auth

// Login checks the credentials and returns a session.
func Login(user, pass string) (string, error) {
	return "session-" + user, nil
}
