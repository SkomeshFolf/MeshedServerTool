import React from "react";
import ReactDOM from "react-dom/client";
import { BrowserRouter, Routes, Route, Navigate, useLocation } from "react-router-dom";
import App from "./App";
import Login from "./pages/Login";
import { AuthProvider, useAuth } from "./auth";
import "./styles.css";

// RequireAuth gates a subtree on having an authenticated user. While the
// auth state is still resolving we render a neutral loader so we don't
// bounce users to /login prematurely.
function RequireAuth({ children }: { children: JSX.Element }) {
  const auth = useAuth();
  const location = useLocation();
  if (auth.loading) {
    return (
      <main>
        <p className="muted">Loading…</p>
      </main>
    );
  }
  if (!auth.authenticated) {
    return <Navigate to="/login" replace state={{ from: location.pathname }} />;
  }
  return children;
}

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <BrowserRouter>
      <AuthProvider>
        <Routes>
          <Route path="/login" element={<Login />} />
          <Route
            path="/*"
            element={
              <RequireAuth>
                <App />
              </RequireAuth>
            }
          />
        </Routes>
      </AuthProvider>
    </BrowserRouter>
  </React.StrictMode>,
);
