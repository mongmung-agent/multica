package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (h *Handler) GetIssueCollaboration(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if h.TaskService == nil || h.TaskService.Collaboration == nil {
		writeJSON(w, http.StatusOK, map[string]any{"collaboration": nil})
		return
	}
	context, err := h.TaskService.Collaboration.WorkroomSnapshot(r.Context(), issue.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load collaboration context")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"collaboration": context})
}
