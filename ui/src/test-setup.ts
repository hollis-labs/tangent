// Vitest setup file — extends `expect` with @testing-library/jest-dom
// matchers and ensures the DOM is reset between tests.
//
// The setup is intentionally minimal: each test owns its own render and
// cleanup is wired through @testing-library/react's auto-cleanup (Vitest
// detects the framework and registers afterEach automatically).
import "@testing-library/jest-dom/vitest";
