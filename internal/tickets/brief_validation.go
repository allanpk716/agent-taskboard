package tickets

import (
	"context"
	"strings"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
	"github.com/allanpk716/agent-taskboard/internal/identity"
	"github.com/allanpk716/agent-taskboard/internal/template"
)

// ValidateRevise 按原说明修订规则做保存草稿前的校验，不写任何工单数据。
func (m *Manager) ValidateRevise(ctx context.Context, viewer identity.Viewer, ref string, in ReviseInput) (Full, error) {
	full, err := m.ForViewer(ctx, viewer, ref)
	if err != nil {
		return Full{}, err
	}
	if viewer.Kind == identity.ViewerGuest {
		if full.Status != "open" {
			return Full{}, ErrTicketClosed
		}
		if !viewer.GuestLink.CanEditBrief {
			return Full{}, ErrLinkCannotEditBrief
		}
	} else if viewer.Member.ID != full.Creator.MemberID {
		return Full{}, ErrNotBriefOwner
	}
	if in.BaseRevision == 0 {
		return Full{}, ErrBaseRevisionRequired
	}
	if in.BaseRevision != full.BriefRevision {
		return Full{}, &RevisionConflictError{BaseRevision: in.BaseRevision, CurrentRevision: full.BriefRevision}
	}
	if in.Title != nil && strings.TrimSpace(*in.Title) == "" {
		return Full{}, ErrTitleRequired
	}
	if in.BriefMD != nil && full.TemplateDef != nil {
		return Full{}, ErrBriefMDWithTemplate
	}
	if in.Fields != nil {
		if full.TemplateDef == nil {
			return Full{}, ErrFieldsWithoutTemplate
		}
		if err = validateFieldsAgainst(full.TemplateDef.Fields, in.Fields); err != nil {
			return Full{}, err
		}
		for _, f := range full.TemplateDef.Fields {
			if f.Type != template.TypeDimension {
				continue
			}
			v := strings.TrimSpace(in.Fields[f.Key])
			if v == "" || v == template.UnsureOption {
				continue
			}
			if err = m.labels.Validate(ctx, []string{f.Dimension + ":" + v}); err != nil {
				return Full{}, err
			}
		}
	}
	_ = attachments.ParsePostRefs(full.BriefMD, full.BriefAttachmentIDs)
	return full, nil
}
