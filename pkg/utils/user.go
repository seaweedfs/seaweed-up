package utils

import (
	"fmt"
	"golang.org/x/term"
	"log"
	"os/user"
	"syscall"
)

// CurrentUser returns current login user
func CurrentUser() string {
	user, err := user.Current()
	if err != nil {
		log.Printf("Get current user: %s", err)
		return "root"
	}
	return user.Username
}

// UserHome returns home directory of current user
func UserHome() string {
	user, err := user.Current()
	if err != nil {
		log.Printf("Get current user home: %s", err)
		return "root"
	}
	return user.HomeDir
}

// return the first non empty string
func Nvl(values ...string) string {
	for _, s := range values {
		if s != "" {
			return s
		}
	}
	return ""
}

// PromptForPassword reads a password input from console
func PromptForPassword(format string, a ...interface{}) string {
	defer fmt.Println("")

	fmt.Printf(format, a...)

	input, err := term.ReadPassword(syscall.Stdin)

	if err != nil {
		return ""
	}
	return string(input)
}
