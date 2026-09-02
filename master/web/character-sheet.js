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

// renderCharacterSheet replaces containerEl's contents with a read-only
// rendering of characterData, per rootSchema's top-level "properties".
// rootSchema is the full parsed JSON Schema document (character.schema_
// response's json_schema, JSON.parse'd); characterData is character.
// state's character_data.
export function renderCharacterSheet(containerEl, rootSchema, characterData) {
  containerEl.innerHTML = "";
  if (!rootSchema || !rootSchema.properties) {
    containerEl.appendChild(textNode("No character schema available."));
    return;
  }
  containerEl.appendChild(renderObject(rootSchema, characterData, rootSchema));
}
