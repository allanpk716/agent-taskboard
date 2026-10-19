package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/allanpk716/agent-taskboard/internal/identity"
	"github.com/allanpk716/agent-taskboard/internal/tickets"
)

type addCollaboratorRequest struct {
	Member assigneeRef `json:"member"`
}

func isCollaboratorDeactivated(err error) bool {
	var e *tickets.CollaboratorDeactivatedError
	return errors.As(err, &e)
}

func (a *API) handleCollaborators(w http.ResponseWriter, r *http.Request, ref string, rest []string) {
	if len(rest) == 1 && rest[0] == "leave" {
		if r.Method != http.MethodPost {
			WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "本人退出用 POST …/collaborators/leave")
			return
		}
		a.leaveCollaborators(w, r, ref)
		return
	}
	if len(rest) == 1 {
		if r.Method != http.MethodDelete {
			WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "移除协作者用 DELETE …/collaborators/{成员id}")
			return
		}
		a.removeCollaborator(w, r, ref, rest[0])
		return
	}
	if len(rest) != 0 {
		WriteError(w, http.StatusNotFound, CodeNotFound, "没有这个接口",
			"协作者接口：POST …/collaborators")
		return
	}
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "加入协作者用 POST …/collaborators")
		return
	}
	a.addCollaborator(w, r, ref)
}

func (a *API) addCollaborator(w http.ResponseWriter, r *http.Request, ref string) {
	viewer, ok := a.viewerFromRequest(w, r)
	if !ok {
		return
	}
	if viewer.Kind != identity.ViewerMember {
		WriteError(w, http.StatusForbidden, CodeMembersOnly, "协作者管理只对成员开放", "访客不能查看或管理协作者")
		return
	}
	var req addCollaboratorRequest
	if !decodeJSON(w, r, &req, `{"member": "成员 id 或姓名（id 也可写成数字，如 4）"}`) {
		return
	}
	if req.Member.invalid {
		writeInvalidAssignee(w)
		return
	}
	a.withIdempotency(w, r, func(w http.ResponseWriter, r *http.Request) {
		result, err := a.tickets.AddCollaborator(r.Context(), viewer, ref, req.Member.ref)
		if err != nil {
			a.writeTicketError(w, r, ref, err)
			return
		}
		writeJSON(w, http.StatusCreated, newCollaboratorsView(result.Full))
	})
}

func newCollaboratorsView(full tickets.Full) struct {
	Collaborators []collaboratorView `json:"collaborators"`
} {
	out := make([]collaboratorView, 0, len(full.Collaborators))
	for _, c := range full.Collaborators {
		cv := collaboratorView{ID: c.ID, Name: c.Name, AddedAt: c.AddedAt}
		if c.Deactivated {
			deactivated := true
			cv.Deactivated = &deactivated
		}
		out = append(out, cv)
	}
	return struct {
		Collaborators []collaboratorView `json:"collaborators"`
	}{Collaborators: out}
}

func (a *API) removeCollaborator(w http.ResponseWriter, r *http.Request, ref, rawID string) {
	viewer, ok := a.memberFromRequest(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id <= 0 {
		WriteError(w, http.StatusUnprocessableEntity, CodeInvalidAssignee, "成员 id 必须是正整数", "")
		return
	}
	a.withIdempotency(w, r, func(w http.ResponseWriter, r *http.Request) {
		result, err := a.tickets.RemoveCollaborator(r.Context(), viewer, ref, id)
		if err != nil {
			a.writeTicketError(w, r, ref, err)
			return
		}
		writeJSON(w, http.StatusOK, newCollaboratorsView(result.Full))
	})
}

func (a *API) leaveCollaborators(w http.ResponseWriter, r *http.Request, ref string) {
	viewer, ok := a.memberFromRequest(w, r)
	if !ok {
		return
	}
	a.withIdempotency(w, r, func(w http.ResponseWriter, r *http.Request) {
		result, err := a.tickets.LeaveCollaborators(r.Context(), viewer, ref)
		if err != nil {
			a.writeTicketError(w, r, ref, err)
			return
		}
		writeJSON(w, http.StatusOK, newCollaboratorsView(result.Full))
	})
}
