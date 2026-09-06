// Copyright (c) 2026 James Duane Plotts. Licensed under the MIT License.
// See LICENSE in the repository root.
//
// layforge.org's lobby page: fetches GET /api/v1/listings (registry's
// own public read endpoint — see internal/lobby/handlers.go) on load
// and on a refresh interval, rendering each currently-live campaign a
// self-hosted Master has opted into listing (master/internal/registry's
// own heartbeat client). Plain HTML/JS, no build step, matching every
// other Layforge web surface's own style (master/web/, admin-web/).

const REFRESH_INTERVAL_MS = 30000;

const el = {
  tableBody: document.getElementById("listings-body"),
  empty: document.getElementById("listings-empty"),
  error: document.getElementById("listings-error"),
};

function levelRangeText(minLevel, maxLevel) {
  if (minLevel && maxLevel) return `${minLevel}–${maxLevel}`;
  if (minLevel) return `${minLevel}+`;
  if (maxLevel) return `up to ${maxLevel}`;
  return "any";
}

function playersText(joined, slots) {
  return slots > 0 ? `${joined}/${slots}` : String(joined);
}

function renderListings(listings) {
  el.tableBody.innerHTML = "";
  el.empty.hidden = listings.length > 0;

  for (const listing of listings) {
    const row = document.createElement("tr");

    const nameCell = document.createElement("td");
    nameCell.textContent = listing.adventure_name;
    row.appendChild(nameCell);

    const levelCell = document.createElement("td");
    levelCell.textContent = levelRangeText(listing.min_level, listing.max_level);
    row.appendChild(levelCell);

    const playersCell = document.createElement("td");
    playersCell.textContent = playersText(listing.players_joined, listing.player_slots);
    row.appendChild(playersCell);

    const lockCell = document.createElement("td");
    lockCell.textContent = listing.password_protected ? "🔒" : "";
    lockCell.title = listing.password_protected ? "Requires a room password from the Host" : "";
    row.appendChild(lockCell);

    const joinCell = document.createElement("td");
    joinCell.className = "join-cell";
    joinCell.textContent = `${listing.join_url} — campaign: ${listing.campaign_id}`;
    row.appendChild(joinCell);

    el.tableBody.appendChild(row);
  }
}

async function loadListings() {
  try {
    const resp = await fetch("/api/v1/listings");
    if (!resp.ok) {
      throw new Error(`server returned ${resp.status}`);
    }
    const data = await resp.json();
    renderListings(data.listings || []);
    el.error.hidden = true;
  } catch (err) {
    el.error.hidden = false;
    el.error.textContent = `Could not load open games: ${err.message}`;
  }
}

loadListings();
setInterval(loadListings, REFRESH_INTERVAL_MS);
