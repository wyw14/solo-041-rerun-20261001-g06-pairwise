package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/wyw14/cry-97/internal/model"
)

type custodyRegisterRequest struct {
	SampleID    string `json:"sample_id"`
	SamplePoint string `json:"sample_point"`
	Sampler     string `json:"sampler"`
	SampledAt   string `json:"sampled_at"`
	SealedAt    string `json:"sealed_at"`
}

type custodyActorRequest struct {
	Actor      string `json:"actor"`
	Receiver   string `json:"receiver"`
	Note       string `json:"note"`
	OccurredAt string `json:"occurred_at"`
}

func (s *Server) custodyViews(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.plant.CustodyViews())
}

func (s *Server) custodyDetail(w http.ResponseWriter, r *http.Request) {
	sampleID, ok := parseCustodySampleID(w, r)
	if !ok {
		return
	}
	chain, found := s.plant.Custody(sampleID)
	if !found {
		writeError(w, http.StatusNotFound, errors.New("custody chain is not found"))
		return
	}
	writeJSON(w, http.StatusOK, chain)
}

func (s *Server) registerCustody(w http.ResponseWriter, r *http.Request) {
	var input custodyRegisterRequest
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	sampleID, err := uuid.Parse(input.SampleID)
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.New("sample id is invalid"))
		return
	}
	sampledAt, sealedAt, err := parseCustodyTimes(input.SampledAt, input.SealedAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	chain, err := s.plant.RegisterCustody(r.Context(), sampleID, input.SamplePoint, input.Sampler, sampledAt, sealedAt)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err)
		return
	}
	writeJSON(w, http.StatusCreated, chain)
}

func (s *Server) receiveCustody(w http.ResponseWriter, r *http.Request) {
	sampleID, ok := parseCustodySampleID(w, r)
	if !ok {
		return
	}
	var input custodyActorRequest
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	receiver := input.Receiver
	if receiver == "" {
		receiver = input.Actor
	}
	if receiver == "" {
		writeError(w, http.StatusBadRequest, errors.New("receiver is required"))
		return
	}
	occurredAt, err := parseOptionalTime(input.OccurredAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	chain, err := s.plant.ReceiveCustody(r.Context(), sampleID, receiver, occurredAt)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, chain)
}

func (s *Server) loseCustody(w http.ResponseWriter, r *http.Request) {
	s.closeCustody(w, r, s.plant.LoseCustody)
}

func (s *Server) revokeCustody(w http.ResponseWriter, r *http.Request) {
	s.closeCustody(w, r, s.plant.RevokeCustody)
}

func (s *Server) closeCustody(
	w http.ResponseWriter, r *http.Request,
	close func(ctx context.Context, sampleID uuid.UUID, actor, note string, at time.Time) (model.ChainOfCustody, error),
) {
	sampleID, ok := parseCustodySampleID(w, r)
	if !ok {
		return
	}
	var input custodyActorRequest
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if input.Actor == "" {
		writeError(w, http.StatusBadRequest, errors.New("actor is required"))
		return
	}
	occurredAt, err := parseOptionalTime(input.OccurredAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	chain, err := close(r.Context(), sampleID, input.Actor, input.Note, occurredAt)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, chain)
}

func parseCustodySampleID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	sampleID, err := uuid.Parse(chi.URLParam(r, "sampleID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.New("sample id is invalid"))
		return uuid.Nil, false
	}
	return sampleID, true
}

func parseCustodyTimes(sampledRaw, sealedRaw string) (time.Time, time.Time, error) {
	sampledAt, err := parseOptionalTime(sampledRaw)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	sealedAt, err := parseOptionalTime(sealedRaw)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	return sampledAt, sealedAt, nil
}

func parseOptionalTime(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, errors.New("time must be RFC3339, e.g. 2026-09-30T08:00:00Z")
	}
	return parsed, nil
}
