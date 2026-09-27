package app

import (
	"context"
	"errors"
	"math/rand/v2"
	"net/mail"
	"regexp"
	"slices"
	"strings"

	"github.com/augustoroman/taskmaster/internal/store"
)

// TagView is a tag as one user sees it.
type TagView struct {
	store.UserTag
	Owner *store.User
}

type ShareView struct {
	*store.Share
	User *store.User // nil until the invitee logs in
}

const maxTagName = 100

// tagPalette is the colors new tags get: Google Docs' "light 1" row (also in
// migration 005 and web/src/colors.ts).
var tagPalette = []string{
	"#cc4125", "#e06666", "#f6b26b", "#ffd966", "#93c47d", "#76a5af", "#6d9eeb", "#6fa8dc", "#8e7cc3", "#c27ba0",
}

var colorRE = regexp.MustCompile(`^#[0-9a-f]{6}$`)

func cleanColor(color string) (string, error) {
	color = strings.ToLower(strings.TrimSpace(color))
	if !colorRE.MatchString(color) {
		return "", invalid("color must look like #a1b2c3")
	}
	return color, nil
}

// pickColor returns a random palette color, preferring ones not in use.
func pickColor(inUse []string) string {
	var unused []string
	for _, c := range tagPalette {
		if !slices.Contains(inUse, c) {
			unused = append(unused, c)
		}
	}
	if len(unused) == 0 {
		unused = tagPalette
	}
	return unused[rand.IntN(len(unused))]
}

func (s *Service) requireTagLevel(tx *store.Tx, u *store.User, tagID string, min store.Level) error {
	level, err := tx.TagLevel(u.ID, tagID)
	if errors.Is(err, store.ErrNotFound) || level == store.LevelNone {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if level < min {
		return ErrPermission
	}
	return nil
}

// ownTag loads a tag that u owns.
func (s *Service) ownTag(tx *store.Tx, u *store.User, tagID string) (*store.Tag, error) {
	if err := s.requireTagLevel(tx, u, tagID, store.LevelRead); err != nil {
		return nil, err
	}
	tag, err := tx.GetTag(tagID)
	if err != nil {
		return nil, err
	}
	if tag.OwnerID != u.ID {
		return nil, ErrPermission
	}
	return tag, nil
}

func (s *Service) ListTags(ctx context.Context, u *store.User) ([]*TagView, error) {
	var views []*TagView
	err := s.db.Tx(ctx, func(tx *store.Tx) error {
		tags, err := tx.UserTags(u.ID)
		if err != nil {
			return err
		}
		var owners []string
		for _, t := range tags {
			owners = append(owners, t.OwnerID)
		}
		users, err := tx.GetUsers(owners)
		if err != nil {
			return err
		}
		for _, t := range tags {
			views = append(views, &TagView{t, users[t.OwnerID]})
		}
		return nil
	})
	return views, err
}

func cleanTagName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", invalid("tag name is required")
	}
	if len(name) > maxTagName {
		return "", invalid("tag name is too long")
	}
	return name, nil
}

func duplicateName(err error, name string) error {
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return invalid("you already have a tag named %q", name)
	}
	return err
}

// CreateTag creates a tag. An empty color picks a pastel one.
func (s *Service) CreateTag(ctx context.Context, u *store.User, name, color string) (*TagView, error) {
	name, err := cleanTagName(name)
	if err != nil {
		return nil, err
	}
	if color != "" {
		if color, err = cleanColor(color); err != nil {
			return nil, err
		}
	}
	tag := &store.Tag{OwnerID: u.ID, Name: name, Color: color, CreatedAt: s.now()}
	err = s.db.Tx(ctx, func(tx *store.Tx) error {
		if tag.Color == "" {
			tags, err := tx.UserTags(u.ID)
			if err != nil {
				return err
			}
			var inUse []string
			for _, t := range tags {
				inUse = append(inUse, t.Color)
			}
			tag.Color = pickColor(inUse)
		}
		return duplicateName(tx.InsertTag(tag), name)
	})
	if err != nil {
		return nil, err
	}
	return &TagView{store.UserTag{Tag: *tag, Level: store.LevelFull}, u}, nil
}

// UpdateTag renames a tag, and recolors it if color is set. Owner only.
func (s *Service) UpdateTag(ctx context.Context, u *store.User, id, name, color string) (*TagView, error) {
	name, err := cleanTagName(name)
	if err != nil {
		return nil, err
	}
	if color != "" {
		if color, err = cleanColor(color); err != nil {
			return nil, err
		}
	}
	var view *TagView
	err = s.db.Tx(ctx, func(tx *store.Tx) error {
		tag, err := s.ownTag(tx, u, id)
		if err != nil {
			return err
		}
		tag.Name = name
		if color != "" {
			tag.Color = color
		}
		if err := duplicateName(tx.UpdateTag(tag), name); err != nil {
			return err
		}
		view = &TagView{store.UserTag{Tag: *tag, Level: store.LevelFull}, u}
		return nil
	})
	return view, err
}

// DeleteTag deletes a tag and removes it from its tasks. Owner only.
func (s *Service) DeleteTag(ctx context.Context, u *store.User, id string) error {
	return s.db.Tx(ctx, func(tx *store.Tx) error {
		if _, err := s.ownTag(tx, u, id); err != nil {
			return err
		}
		return tx.DeleteTag(id)
	})
}

// SetTagColor sets u's color for a tag. For the owner that's the tag's color
// (which new shares start with); for anyone else it's just theirs.
func (s *Service) SetTagColor(ctx context.Context, u *store.User, id, color string) error {
	color, err := cleanColor(color)
	if err != nil {
		return err
	}
	return s.db.Tx(ctx, func(tx *store.Tx) error {
		if err := s.requireTagLevel(tx, u, id, store.LevelRead); err != nil {
			return err
		}
		tag, err := tx.GetTag(id)
		if err != nil {
			return err
		}
		if tag.OwnerID == u.ID {
			tag.Color = color
			return tx.UpdateTag(tag)
		}
		return tx.SetTagColor(u.ID, id, color)
	})
}

func (s *Service) SetTagHidden(ctx context.Context, u *store.User, id string, hidden bool) error {
	return s.db.Tx(ctx, func(tx *store.Tx) error {
		if err := s.requireTagLevel(tx, u, id, store.LevelRead); err != nil {
			return err
		}
		return tx.SetTagHidden(u.ID, id, hidden)
	})
}

// ListShares requires full access to the tag.
func (s *Service) ListShares(ctx context.Context, u *store.User, tagID string) ([]*ShareView, error) {
	var views []*ShareView
	err := s.db.Tx(ctx, func(tx *store.Tx) error {
		if err := s.requireTagLevel(tx, u, tagID, store.LevelFull); err != nil {
			return err
		}
		shares, err := tx.ListShares(tagID)
		if err != nil {
			return err
		}
		views, err = shareViews(tx, shares)
		return err
	})
	return views, err
}

func shareViews(tx *store.Tx, shares []*store.Share) ([]*ShareView, error) {
	var ids []string
	for _, sh := range shares {
		if sh.UserID != "" {
			ids = append(ids, sh.UserID)
		}
	}
	users, err := tx.GetUsers(ids)
	if err != nil {
		return nil, err
	}
	views := make([]*ShareView, len(shares))
	for i, sh := range shares {
		views[i] = &ShareView{sh, users[sh.UserID]}
	}
	return views, nil
}

func validLevel(level store.Level) error {
	if level < store.LevelRead || level > store.LevelFull {
		return invalid("invalid access level")
	}
	return nil
}

// ShareTag shares a tag with an email, inviting them if they have no account.
// Sharing again with the same email changes the level. Requires full access.
func (s *Service) ShareTag(ctx context.Context, u *store.User, tagID, email string, level store.Level) (*ShareView, error) {
	if err := validLevel(level); err != nil {
		return nil, err
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Name != "" {
		return nil, invalid("invalid email address %q", email)
	}
	email = store.NormalizeEmail(addr.Address)
	var view *ShareView
	err = s.db.Tx(ctx, func(tx *store.Tx) error {
		if err := s.requireTagLevel(tx, u, tagID, store.LevelFull); err != nil {
			return err
		}
		tag, err := tx.GetTag(tagID)
		if err != nil {
			return err
		}
		invitee, err := tx.GetUserByEmail(email)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if invitee != nil && invitee.ID == tag.OwnerID {
			return invalid("%s owns this tag", email)
		}
		share, err := tx.GetShareByEmail(tagID, email)
		switch {
		case err == nil:
			share.Level = level
			if err := tx.UpdateShareLevel(share.ID, level); err != nil {
				return err
			}
		case errors.Is(err, store.ErrNotFound):
			// The recipient starts with the sharer's color.
			color, err := s.userTagColor(tx, u, tagID)
			if err != nil {
				return err
			}
			share = &store.Share{TagID: tagID, Email: email, Level: level, CreatedBy: u.ID, CreatedAt: s.now(), Color: color}
			if invitee != nil {
				share.UserID = invitee.ID
			}
			if err := tx.InsertShare(share); err != nil {
				return err
			}
		default:
			return err
		}
		views, err := shareViews(tx, []*store.Share{share})
		if err != nil {
			return err
		}
		view = views[0]
		return nil
	})
	return view, err
}

// userTagColor is the color u sees for a tag.
func (s *Service) userTagColor(tx *store.Tx, u *store.User, tagID string) (string, error) {
	tags, err := tx.UserTags(u.ID)
	if err != nil {
		return "", err
	}
	for _, t := range tags {
		if t.ID == tagID {
			return t.Color, nil
		}
	}
	return "", ErrNotFound
}

func (s *Service) UpdateShare(ctx context.Context, u *store.User, id string, level store.Level) (*ShareView, error) {
	if err := validLevel(level); err != nil {
		return nil, err
	}
	var view *ShareView
	err := s.db.Tx(ctx, func(tx *store.Tx) error {
		share, err := s.loadShare(tx, u, id, false)
		if err != nil {
			return err
		}
		share.Level = level
		if err := tx.UpdateShareLevel(id, level); err != nil {
			return err
		}
		views, err := shareViews(tx, []*store.Share{share})
		if err != nil {
			return err
		}
		view = views[0]
		return nil
	})
	return view, err
}

// RevokeShare removes a share. Requires full access to the tag, unless it is
// your own share (leaving the tag).
func (s *Service) RevokeShare(ctx context.Context, u *store.User, id string) error {
	return s.db.Tx(ctx, func(tx *store.Tx) error {
		if _, err := s.loadShare(tx, u, id, true); err != nil {
			return err
		}
		return tx.DeleteShare(id)
	})
}

func (s *Service) loadShare(tx *store.Tx, u *store.User, id string, allowOwn bool) (*store.Share, error) {
	share, err := tx.GetShare(id)
	if err != nil {
		return nil, ErrNotFound
	}
	if allowOwn && share.UserID == u.ID {
		return share, nil
	}
	if err := s.requireTagLevel(tx, u, share.TagID, store.LevelFull); err != nil {
		return nil, err
	}
	return share, nil
}
