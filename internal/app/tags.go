package app

import (
	"context"
	"errors"
	"net/mail"
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

func (s *Service) CreateTag(ctx context.Context, u *store.User, name, color string) (*TagView, error) {
	name, err := cleanTagName(name)
	if err != nil {
		return nil, err
	}
	tag := &store.Tag{OwnerID: u.ID, Name: name, Color: color, CreatedAt: s.now()}
	err = s.db.Tx(ctx, func(tx *store.Tx) error {
		return duplicateName(tx.InsertTag(tag), name)
	})
	if err != nil {
		return nil, err
	}
	return &TagView{store.UserTag{Tag: *tag, Level: store.LevelFull}, u}, nil
}

// UpdateTag renames or recolors a tag. Owner only.
func (s *Service) UpdateTag(ctx context.Context, u *store.User, id, name, color string) (*TagView, error) {
	name, err := cleanTagName(name)
	if err != nil {
		return nil, err
	}
	var view *TagView
	err = s.db.Tx(ctx, func(tx *store.Tx) error {
		tag, err := s.ownTag(tx, u, id)
		if err != nil {
			return err
		}
		tag.Name, tag.Color = name, color
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
			share = &store.Share{TagID: tagID, Email: email, Level: level, CreatedBy: u.ID, CreatedAt: s.now()}
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
