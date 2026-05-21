// Package ui holds shared HTML fragments used across anyserver's page
// templates.  Centralizing the footer (and any future shared chrome)
// here keeps every page visually consistent without circular imports
// between the root anyserver package and internal sub-packages.
package ui

// Footer is the all-footer block embedded at the bottom of every
// rendered page (index, /docs/, /api/, /server/, placeholders).
//
// Behaviour is CSS-only:
//   - The `#menu-collapse` checkbox toggles `.footer-menu-links` via
//     a sibling combinator and flips the toggle glyph >> ↔ <<.
//   - `:target` on a `<p>` inside `.footer-content` activates the
//     frunk (display:block via :has), populates the `.frunk-title`
//     and `.frunk-submenu` for that link, and reveals the matching
//     `<p>`.
//   - `.footer-close` is a plain `<a href="#">` that clears the URL
//     fragment, which hides the frunk.
//
// The template expects `.RepoName` in the data passed to Execute.
const Footer = `<footer class="all-footer">
  <input type="checkbox" id="menu-collapse" class="menu-collapse-toggle" aria-hidden="true">
  <div class="footer-base">
    <div class="footer-controls">
      <img class="spinner-disk" src="/static/accretion_256.webp" alt="" aria-hidden="true">
      <span><b>{{.RepoName}}</b> — Status: <b>Alpha</b> — Built 2026 — Free &amp; Open Source by <a href="https://accretional.com/" target="_blank" rel="noopener">Accretional</a> ©</span>
    </div>
    <div class="footer-menu">
      <label for="menu-collapse" class="menu-toggle" aria-label="Collapse menu"></label>
      <span class="footer-menu-links">
        <a href="#about">About</a>
        <a href="#privacy">Privacy</a>
        <a href="#terms">Terms</a>
        <a href="#api">API</a>
        <a href="#docs">Docs</a>
        <a href="#accretional">Accretional</a>
      </span>
    </div>
  </div>
  <div class="footer-content">
    <div class="frunk-header">
      <div class="frunk-submenu">
        <div data-for="api">
          <a href="/api/">Swagger UI</a>
          <a href="/api/swagger.json">swagger.json</a>
        </div>
        <div data-for="docs">
          <a href="/docs/">Package docs</a>
          <a href="/source/">Source browser</a>
        </div>
        <div data-for="accretional">
          <a href="https://accretional.com" target="_blank" rel="noopener">accretional.com</a>
          <a href="https://github.com/accretional" target="_blank" rel="noopener">GitHub</a>
        </div>
      </div>
      <span class="frunk-title"></span>
      <a href="#" class="footer-close" aria-label="Close"></a>
    </div>
    <p data-attr="About anyserver" id="about">anyserver is a composable gRPC+HTTP server framework for Go — built-in source browsing, API docs, server metrics, and a live console wormhole. Open source, MIT-licensed, served from a single embedded binary.</p>
    <p data-attr="Privacy Policy" id="privacy">This Privacy Policy explains how Accretional ("we", "our", or "company") collects, uses, and shares your personal information when you use our website and services.

Information We Collect

We may collect personal information in the following categories:

    Identifiers: Name, email address, postal address, phone number, and similar.
    Commercial Information: Purchase history of products or services.
    Internet Activity: Browsing history, search history, and information on your interaction with our website.
    Geolocation Data: Geographic location information.

How We Use This Information

We may use the information we collect for the following purposes:

    To provide and improve our services
    To provide customer service
    To send marketing and promotional materials
    To ensure the security of our website and services

Sharing of Information

We do not sell or share your personal information with third parties except in the following circumstances: with your consent, with our service providers, as required by law, or in the event of a company sale or merger.

Contact Us

Email: hello@accretional.com</p>
    <p data-attr="Terms and Conditions" id="terms">These Terms of Use establish the terms and conditions for your use of the Accretional website and services. By using our site, you agree to these terms.

Use of Services

When using our website and services, you must comply with all applicable laws and regulations, not violate the rights of other users, not engage in activities that would damage or disrupt our services, and not attempt unauthorized access, data collection, or manipulation of our systems.

Intellectual Property

Our website and its content are protected by intellectual property rights. Users may use our content for personal and non-commercial purposes, but may not modify, copy, distribute, or sell the content without prior express written permission.

Disclaimer

Our services are provided "as is." We make no warranties and disclaim responsibility for any damage or loss that may result from the use of our services.

Governing Law

These Terms of Use are governed by and construed in accordance with the laws of the United States.

Contact Us

Email: hello@accretional.com</p>
    <p data-attr="API Reference" id="api">REST and gRPC endpoints generated from .proto definitions, served from /api/. Browse the Swagger UI for interactive request building, or grab the raw OpenAPI spec at /api/swagger.json.</p>
    <p data-attr="Documentation" id="docs">Package godoc rendered to static HTML at /docs/, with cross-links into the source browser at /source/. README.md is rendered above on the index page.</p>
    <p data-attr="Accretional" id="accretional">We're cool I promise.</p>
  </div>
</footer>`
