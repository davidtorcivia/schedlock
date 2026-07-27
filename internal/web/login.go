package web

import (
	"net/http"

	"github.com/dtorcivia/schedlock/internal/database"
	"github.com/dtorcivia/schedlock/internal/engine"
	"github.com/dtorcivia/schedlock/internal/util"
)

// Login shows the sign-in page.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	if session, _ := h.SessionManager.ValidateSession(r.Context(), sessionIDFrom(r)); session != nil {
		http.Redirect(w, r, SafeRedirect(r.URL.Query().Get("redirect"), "/dashboard"), http.StatusSeeOther)
		return
	}

	h.render(w, r, "login.html", pageData{
		"Title":    "Sign in",
		"Redirect": SafeRedirect(r.URL.Query().Get("redirect"), ""),
	})
}

// LoginSubmit authenticates an operator.
func (h *Handler) LoginSubmit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	clientIP := h.ClientIP(r)
	redirect := SafeRedirect(r.FormValue("redirect"), "/dashboard")

	if !h.loginLimiter.Allow(clientIP) {
		util.Warn("Login rate limit reached", "client_ip", clientIP)
		h.renderLoginError(w, r, redirect, "Too many attempts. Please wait a few minutes and try again.")
		return
	}

	if !h.SessionManager.VerifyPassword(r.FormValue("password")) {
		h.AuditLogger.Log(ctx, engine.Entry{
			EventType: database.AuditLoginFailed,
			Actor:     "web",
			IPAddress: clientIP,
		})
		util.Warn("Failed login attempt", "client_ip", clientIP)
		// The message is deliberately identical whichever way authentication
		// failed, so it reveals nothing about the configured password.
		h.renderLoginError(w, r, redirect, "Incorrect password.")
		return
	}

	h.loginLimiter.Reset(clientIP)

	session, err := h.SessionManager.CreateSession(ctx, "admin", clientIP, r.UserAgent())
	if err != nil {
		util.Error("Failed to create session", "error", err)
		h.renderLoginError(w, r, redirect, "Could not start a session. Please try again.")
		return
	}

	SetSessionCookie(w, session.ID, h.useTLS(), h.SessionManager.SessionDuration())

	h.AuditLogger.Log(ctx, engine.Entry{
		EventType: database.AuditLoginSuccess,
		Actor:     "web:admin",
		IPAddress: clientIP,
	})

	http.Redirect(w, r, redirect, http.StatusSeeOther)
}

// Logout ends the session.
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	if sessionID := sessionIDFrom(r); sessionID != "" {
		if err := h.SessionManager.DeleteSession(r.Context(), sessionID); err != nil {
			util.Warn("Failed to delete session", "error", err)
		}
		h.AuditLogger.Log(r.Context(), engine.Entry{
			EventType: database.AuditLogout,
			Actor:     "web:admin",
			IPAddress: h.ClientIP(r),
		})
	}

	ClearSessionCookie(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (h *Handler) renderLoginError(w http.ResponseWriter, r *http.Request, redirect, message string) {
	h.renderStatus(w, r, http.StatusUnauthorized, "login.html", pageData{
		"Title":    "Sign in",
		"Error":    message,
		"Redirect": redirect,
	})
}
