package api

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/wyw14/cry-97/internal/custody"
	"github.com/wyw14/cry-97/internal/model"
)

type custodyTaskRequest struct {
	LineID   string `json:"line_id"`
	SampleID string `json:"sample_id"`
	Point    string `json:"point"`
	Sampler  string `json:"sampler"`
}

func (s *Server) createCustodyTask(w http.ResponseWriter, r *http.Request) {
	var input custodyTaskRequest
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	lineID, err := model.ParseLineID(input.LineID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	sampleID, err := uuid.Parse(input.SampleID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	task, err := s.plant.CollectSample(r.Context(), lineID, sampleID, input.Point, input.Sampler)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusCreated, task)
}

type custodyHandoverRequest struct {
	Action string `json:"action"`
	Actor  string `json:"actor"`
	Note   string `json:"note"`
}

func (s *Server) handoverCustodyTask(w http.ResponseWriter, r *http.Request) {
	taskID, err := uuid.Parse(chi.URLParam(r, "taskID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var input custodyHandoverRequest
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	action, err := custody.ParseAction(input.Action)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	task, err := s.plant.HandoverCustody(r.Context(), taskID, action, input.Actor, input.Note)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (s *Server) custodyTask(w http.ResponseWriter, r *http.Request) {
	taskID, err := uuid.Parse(chi.URLParam(r, "taskID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	task, ok := s.plant.CustodyTask(taskID)
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("custody task is not found"))
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (s *Server) custodyPending(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"tasks": s.plant.CustodyPending()})
}

func (s *Server) custodyArchived(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"tasks": s.plant.CustodyArchived()})
}

func (s *Server) custodyList(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"tasks": s.plant.CustodyTasks()})
}
