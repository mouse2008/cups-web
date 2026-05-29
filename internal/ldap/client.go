package ldap

import (
	"context"
	"fmt"
	"strings"

	gldap "github.com/go-ldap/ldap/v3"
)

type DirectoryUser struct {
	Username    string
	UID         string
	DN          string
	DisplayName string
	Email       string
	Phone       string
}

type Client interface {
	SearchUser(ctx context.Context, cfg Config, username string) ([]DirectoryUser, error)
	BindUser(ctx context.Context, cfg Config, dn string, password string) error
	SearchAllUsers(ctx context.Context, cfg Config) ([]DirectoryUser, error)
}

type DialURLFunc func(addr string, opts ...gldap.DialOpt) (*gldap.Conn, error)

type LDAPClient struct {
	dialURL DialURLFunc
}

func NewClient() Client {
	return &LDAPClient{dialURL: gldap.DialURL}
}

func (c *LDAPClient) SearchUser(ctx context.Context, cfg Config, username string) ([]DirectoryUser, error) {
	conn, err := c.openAndBind(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	filter := fmt.Sprintf("(&%s(%s=%s))", normalizeFilter(cfg.UserFilter), cfg.LoginAttr, gldap.EscapeFilter(username))
	return searchDirectoryUsers(ctx, conn, cfg, filter)
}

func (c *LDAPClient) BindUser(ctx context.Context, cfg Config, dn string, password string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	conn, err := c.open(ctx, cfg)
	if err != nil {
		return err
	}
	defer conn.Close()

	return conn.Bind(dn, password)
}

func (c *LDAPClient) SearchAllUsers(ctx context.Context, cfg Config) ([]DirectoryUser, error) {
	conn, err := c.openAndBind(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	return searchDirectoryUsers(ctx, conn, cfg, normalizeFilter(cfg.UserFilter))
}

func (c *LDAPClient) open(ctx context.Context, cfg Config) (*gldap.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.dialURL == nil {
		c.dialURL = gldap.DialURL
	}
	return c.dialURL(cfg.URL)
}

func (c *LDAPClient) openAndBind(ctx context.Context, cfg Config) (*gldap.Conn, error) {
	conn, err := c.open(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if cfg.BindDN == "" {
		return conn, nil
	}
	if err := conn.Bind(cfg.BindDN, cfg.BindPassword); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func searchDirectoryUsers(ctx context.Context, conn *gldap.Conn, cfg Config, filter string) ([]DirectoryUser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	req := gldap.NewSearchRequest(
		cfg.BaseDN,
		gldap.ScopeWholeSubtree,
		gldap.NeverDerefAliases,
		0,
		0,
		false,
		filter,
		requestedAttributes(cfg),
		nil,
	)

	res, err := conn.Search(req)
	if err != nil {
		return nil, err
	}

	users := make([]DirectoryUser, 0, len(res.Entries))
	for _, entry := range res.Entries {
		users = append(users, directoryUserFromEntry(entry, cfg))
	}
	return users, nil
}

func directoryUserFromEntry(entry *gldap.Entry, cfg Config) DirectoryUser {
	return DirectoryUser{
		Username:    strings.TrimSpace(entry.GetAttributeValue(cfg.LoginAttr)),
		UID:         directoryUID(entry, cfg),
		DN:          strings.TrimSpace(entry.DN),
		DisplayName: strings.TrimSpace(entry.GetAttributeValue(cfg.DisplayNameAttr)),
		Email:       strings.TrimSpace(entry.GetAttributeValue(cfg.EmailAttr)),
		Phone:       strings.TrimSpace(entry.GetAttributeValue(cfg.PhoneAttr)),
	}
}

func directoryUID(entry *gldap.Entry, cfg Config) string {
	for _, attr := range []string{"entryUUID", "uidNumber", "uid", cfg.LoginAttr} {
		if value := strings.TrimSpace(entry.GetAttributeValue(attr)); value != "" {
			return value
		}
	}
	return ""
}

func requestedAttributes(cfg Config) []string {
	seen := make(map[string]struct{}, 8)
	attrs := make([]string, 0, 8)
	for _, attr := range []string{
		cfg.LoginAttr,
		cfg.DisplayNameAttr,
		cfg.EmailAttr,
		cfg.PhoneAttr,
		"entryUUID",
		"uidNumber",
		"uid",
	} {
		attr = strings.TrimSpace(attr)
		if attr == "" {
			continue
		}
		if _, ok := seen[attr]; ok {
			continue
		}
		seen[attr] = struct{}{}
		attrs = append(attrs, attr)
	}
	return attrs
}

func normalizeFilter(filter string) string {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return "(objectClass=person)"
	}
	if strings.HasPrefix(filter, "(") && strings.HasSuffix(filter, ")") {
		return filter
	}
	return "(" + filter + ")"
}
