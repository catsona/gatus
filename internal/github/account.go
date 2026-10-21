package github

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"git.pegasko.art/catsona/gstatus/internal/model"
)

// Credentials resolves effective gh credentials without persisting or logging them.
func (c *Client) Credentials(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	value, err := c.output(ctx, "", "auth", "token", "--hostname", "github.com")
	if err != nil {
		return "", err
	}

	token := strings.TrimSpace(string(value))
	if token == "" {
		return "", fmt.Errorf("gh returned an empty authentication token")
	}

	return token, nil
}

// Account verifies the owner of the supplied token rather than the mutable gh default.
func (c *Client) Account(ctx context.Context, token string) (model.Account, error) {
	var user struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
	}
	if err := c.api(ctx, token, "user", &user); err != nil {
		return model.Account{}, err
	}

	if user.ID == 0 || user.Login == "" {
		return model.Account{}, fmt.Errorf("GitHub returned an incomplete user identity")
	}

	return model.Account{ID: strconv.FormatInt(user.ID, 10), Login: user.Login, Host: "github.com"}, nil
}
