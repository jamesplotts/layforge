// Copyright (c) 2026 James Duane Plotts. Licensed under the MIT License.
// See LICENSE in the repository root.
//
// A generic JSON Schema (draft 2020-12) -> DOM renderer for
// character.state's character_data, driven by whatever
// character.schema_response's json_schema actually declares — not
// hardcoded to D&D's ability-scores/hit-points shape (design doc §4:
// "this matters even in V1 because retrofitting a hardcoded UI later...
// is expensive"). Handles the schema constructs OpenCombatEngine's own
// schema (see CharacterSchema.cs in the OpenCombatEngine repo) actually
// uses — object/array/string/integer/boolean/number types, "properties",
// "items", and "$ref" against "$defs" — not the full JSON Schema spec
// (no oneOf/anyOf/patternProperties/etc.), since nothing today needs
// more than that and building it speculatively would be guessing at a
// shape instead of learning it from a real second system engine.

const FIELD_LABEL_OVERRIDES = { id: "ID" };

function humanizeFieldName(name) {
  if (FIELD_LABEL_OVERRIDES[name]) return FIELD_LABEL_OVERRIDES[name];
  const spaced = name.replace(/([a-z0-9])([A-Z])/g, "$1 $2");
  return spaced.charAt(0).toUpperCase() + spaced.slice(1);
}

function resolveSchema(schema, rootSchema) {
  if (schema && schema.$ref) {
    const path = schema.$ref.replace(/^#\//, "").split("/");
    let node = rootSchema;
    for (const segment of path) node = node && node[segment];
    if (!node) throw new Error(`character sheet: unresolved $ref ${schema.$ref}`);
    return node;
  }
  return schema;
}

function schemaTypes(schema) {
  if (!schema.type) return [];
  return Array.isArray(schema.type) ? schema.type : [schema.type];
}

function fieldRow(label, valueEl) {
  const row = document.createElement("div");
  row.className = "sheet-field";
  const labelEl = document.createElement("span");
  labelEl.className = "sheet-field-label";
  labelEl.textContent = label;
  const valueWrap = document.createElement("span");
  valueWrap.className = "sheet-field-value";
  valueWrap.appendChild(valueEl);
  row.append(labelEl, valueWrap);
  return row;
}

function textNode(text) {
  const span = document.createElement("span");
  span.textContent = text;
  return span;
}

// renderValue renders one schema-described value (of any type) into a
// DOM node. data may be null/undefined (an absent optional field).
function renderValue(schema, data, rootSchema) {
  schema = resolveSchema(schema, rootSchema);

  if (data === null || data === undefined) return textNode("—");

  const types = schemaTypes(schema);
  if (types.includes("object") && schema.properties) return renderObject(schema, data, rootSchema);
  if (types.includes("array")) return renderArray(schema, data, rootSchema);
  if (typeof data === "boolean") return textNode(data ? "Yes" : "No");
  return textNode(String(data));
}

function renderObject(schema, data, rootSchema) {
  const container = document.createElement("div");
  container.className = "sheet-object";
  for (const propName of Object.keys(schema.properties)) {
    const value = data[propName];
    if (value === null || value === undefined) continue; // omit absent optional fields rather than showing a wall of "—"
    container.appendChild(fieldRow(humanizeFieldName(propName), renderValue(schema.properties[propName], value, rootSchema)));
  }
  if (!container.children.length) return textNode("—");
  return container;
}

function renderArray(schema, data, rootSchema) {
  if (!Array.isArray(data) || data.length === 0) return textNode("none");
  const list = document.createElement("ul");
  list.className = "sheet-array";
  for (const item of data) {
    const li = document.createElement("li");
    li.appendChild(schema.items ? renderValue(schema.items, item, rootSchema) : textNode(String(item)));
    list.appendChild(li);
  }
  return list;
}

// isGroupableProperty reports whether a top-level schema property has
// enough structure of its own to deserve its own sidebar tab — an object
// with properties (abilityScores, hitPoints, inventory's item shape,
// ...) or an array whose items are such an object. A property that's
// just a scalar or an array of scalars (e.g. a string-enum resistances
// list) stays on the Overview tab instead — pulling it out into its own
// single-row tab would just be a worse version of Overview, not a real
// grouping. This is a structural test only, driven entirely by shape —
// nothing here matches on a property *name* like "spells" or
// "inventory", so a schema this has never seen still tabs sensibly (see
// this file's top-of-file doc comment on why that matters).
function isGroupableProperty(schema, rootSchema) {
  const resolved = resolveSchema(schema, rootSchema);
  const types = schemaTypes(resolved);
  if (types.includes("object") && resolved.properties && Object.keys(resolved.properties).length) return true;
  if (types.includes("array") && resolved.items) {
    const itemSchema = resolveSchema(resolved.items, rootSchema);
    const itemTypes = schemaTypes(itemSchema);
    if (itemTypes.includes("object") && itemSchema.properties && Object.keys(itemSchema.properties).length) return true;
  }
  return false;
}

// renderPropsSubset renders just propNames out of schema.properties, in
// the same field-row style renderObject uses for the whole schema.
// singleUnlabeled collapses the common one-property-tab case (e.g. the
// "Ability Scores" tab showing only the abilityScores property) down to
// the property's own value with no redundant repeated label — the tab
// button already names it.
function renderPropsSubset(schema, data, propNames, rootSchema, singleUnlabeled) {
  if (singleUnlabeled && propNames.length === 1) {
    const propName = propNames[0];
    const value = data ? data[propName] : undefined;
    if (value === null || value === undefined) return textNode("—");
    return renderValue(schema.properties[propName], value, rootSchema);
  }
  const container = document.createElement("div");
  container.className = "sheet-object";
  for (const propName of propNames) {
    const value = data ? data[propName] : undefined;
    if (value === null || value === undefined) continue; // omit absent optional fields rather than showing a wall of "—"
    container.appendChild(fieldRow(humanizeFieldName(propName), renderValue(schema.properties[propName], value, rootSchema)));
  }
  if (!container.children.length) return textNode("—");
  return container;
}

// renderCharacterSheetTabs replaces tabsEl/panelsEl's contents with a
// tabbed rendering of characterData, per rootSchema's top-level
// "properties" (character.schema_response's json_schema, JSON.parse'd;
// characterData is character.state's character_data). Every top-level
// scalar property (id, name, team, a resistances list, ...) lands on a
// always-present "Overview" tab; every top-level property with its own
// object/array-of-object shape gets its own tab, labeled from the
// property name alone — see isGroupableProperty's doc comment for why
// that's schema-driven rather than a hardcoded "Stats/Abilities/Spells/
// Inventory" tab list. Safe to call repeatedly (e.g. after every
// character.state) — it re-derives the same tab set from the same
// schema each time and keeps whichever tab was already selected,
// tracked via panelsEl.dataset.activeTab, rather than always resetting
// to Overview.
export function renderCharacterSheetTabs(tabsEl, panelsEl, rootSchema, characterData) {
  const previousActive = panelsEl.dataset.activeTab || "overview";
  tabsEl.innerHTML = "";
  panelsEl.innerHTML = "";
  if (!rootSchema || !rootSchema.properties) {
    panelsEl.appendChild(textNode("No character schema available."));
    return;
  }

  const overviewProps = [];
  const groupedPropNames = [];
  for (const propName of Object.keys(rootSchema.properties)) {
    if (isGroupableProperty(rootSchema.properties[propName], rootSchema)) {
      groupedPropNames.push(propName);
    } else {
      overviewProps.push(propName);
    }
  }

  const tabs = [{ id: "overview", label: "Overview", propNames: overviewProps }];
  for (const propName of groupedPropNames) {
    tabs.push({ id: propName, label: humanizeFieldName(propName), propNames: [propName] });
  }

  const activeId = tabs.some((tab) => tab.id === previousActive) ? previousActive : "overview";
  panelsEl.dataset.activeTab = activeId;

  function selectTab(tabId) {
    panelsEl.dataset.activeTab = tabId;
    for (const button of tabsEl.children) button.classList.toggle("active", button.dataset.tabId === tabId);
    for (const panel of panelsEl.children) panel.hidden = panel.dataset.tabId !== tabId;
  }

  for (const tab of tabs) {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "tab-button" + (tab.id === activeId ? " active" : "");
    button.dataset.tabId = tab.id;
    button.textContent = tab.label;
    button.addEventListener("click", () => selectTab(tab.id));
    tabsEl.appendChild(button);

    const panel = document.createElement("div");
    panel.className = "tab-panel";
    panel.dataset.tabId = tab.id;
    panel.hidden = tab.id !== activeId;
    panel.appendChild(renderPropsSubset(rootSchema, characterData, tab.propNames, rootSchema, tab.id !== "overview"));
    panelsEl.appendChild(panel);
  }
}
