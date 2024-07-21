package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	config "streamingconfig"
)

type server struct {
	lgr  *slog.Logger
	repo *config.WatchedRepo[*conf]
}

// latestConfigHandler returns the latest configuration
func (s *server) latestConfigHandler(w http.ResponseWriter, r *http.Request) {
	latestVersion, err := s.repo.GetLatestVersion()
	if err != nil {
		s.lgr.With("error", err).ErrorContext(r.Context(), "getting latest")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(latestVersion); err != nil {
		s.lgr.With("error", err).ErrorContext(r.Context(), "encoding response")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
}

// putConfigHandler returns a specific config version
func (s *server) putConfigHandler(w http.ResponseWriter, r *http.Request) {
	userID := r.Header.Get("user-id")
	if userID == "" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	// decode input config
	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.lgr.With("error", err).ErrorContext(r.Context(), "reading body payload")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	// Close the body to avoid leaks
	defer r.Body.Close()

	cfg := new(conf)
	err = json.Unmarshal(body, cfg)
	if err != nil {
		s.lgr.With("error", err).ErrorContext(r.Context(), "unmarshalling request into configuration")
		w.WriteHeader(http.StatusInternalServerError)
		// Handle JSON parsing error
		return
	}

	updated, err := s.repo.UpdateConfig(r.Context(), config.UpdateConfigCmd[*conf]{
		By:     userID,
		Config: cfg,
	})
	if err != nil {
		s.lgr.With("error", err).ErrorContext(r.Context(), "updating configuration")
		w.WriteHeader(http.StatusInternalServerError)
		// Handle JSON parsing error
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(updated); err != nil {
		s.lgr.With("error", err).ErrorContext(r.Context(), "encoding response")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
}

// listConfigsHandler returns configs between versions (fromVersion and toVersion)
func (s *server) listConfigsHandler(w http.ResponseWriter, r *http.Request) {
	fromVersionStr := r.URL.Query().Get("fromVersion")
	toVersionStr := r.URL.Query().Get("toVersion")
	if fromVersionStr == "" || toVersionStr == "" {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Missing fromVersion or toVersion parameter")
		return
	}
	fromVersion, err := strconv.ParseUint(fromVersionStr, 0, 32)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "fromVersion must be a non-negative integer")
		s.lgr.With("error", err).ErrorContext(r.Context(), fmt.Sprintf("parsing from-version string %s", fromVersionStr))
		return
	}
	toVersion, err := strconv.ParseUint(toVersionStr, 0, 32)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "toVersion must be a non-negative integer")
		s.lgr.With("error", err).ErrorContext(r.Context(), fmt.Sprintf("parsing to-version string %s", toVersionStr))
		return
	}
	versions, err := s.repo.ListVersionedConfigs(r.Context(), config.ListVersionedConfigsQuery{
		FromVersion: uint32(fromVersion),
		ToVersion:   uint32(toVersion),
	})
	if err != nil {
		s.lgr.With("error", err).ErrorContext(r.Context(), "listing versions")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(versions); err != nil {
		s.lgr.With("error", err).ErrorContext(r.Context(), "encoding response")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
}
