# Tile38-Plus: Technical Implementation Details

This document catalogs the technical modifications made to the core Tile38 codebase to support the custom features in Tile38-Plus, specifically **Speed Limits (`SPEEDLIMIT`)** in geofences and **Chronological Rejection (`NEWER`)** for updates.

## 1. Speed Limit Geofences (`SPEEDLIMIT`)

### Motivation
To natively handle vehicle velocity boundaries and trigger events without external polling or stream processors.

### Core Files Modified
* **`internal/server/token.go`**:
  * **Functionality:** Extended the search argument parser to accept the `SPEEDLIMIT` keyword. 
  * **Code Impact:** When scanning query tokens (in the `where` and `limit` clauses), the engine now parses two adjacent tokens if `SPEEDLIMIT` is detected: a float indicating the limit (`speedLimit`) and a string indicating the JSON field name that holds the speed data (`speedField`).
  
* **`internal/server/search.go`**:
  * **Functionality:** Updated the internal struct representations of geofences.
  * **Code Impact:** Added `speedLimit float64` and `speedField string` attributes inside the standard parameter structs (`searchScanBaseTokens`, which is embedded in `liveFenceSwitches`) so they are preserved in memory while the fence runs.

* **`internal/server/fence.go`**:
  * **Functionality:** The evaluation engine that computes if an object transition triggers a webhook/pubsub message.
  * **Code Impact:** Added logic inside the `fenceMatch` function. 
    1. During processing, it reads the previous state (`details.old`) and the new state (`details.obj`).
    2. Using the `fence.speedField`, it extracts the historical speed and the new speed (`f.Value().Num()`).
    3. It performs a threshold comparison against the configured `fence.speedLimit`.
    4. If the spatial coordinate is evaluated as `inside` the fence AND the vehicle crosses from `speed <= limit` to `speed > limit`, an `overspeed` notification string is constructed. Conversely, if it crosses from `speed > limit` to `speed <= limit`, an `underspeed` notification is triggered.

* **`internal/server/hooks.go`**:
  * **Functionality:** Added `overspeed` and `underspeed` to the allowed `detect` types map.

---

## 2. Chronological Data Exclusion (`NEWER`)

### Motivation
To drop out-of-order vehicle tracking events natively inside the database, preventing older packages from overwriting recent ones.

### Core Files Modified
* **`internal/server/crud.go`** (Specifically `cmdSET` and `cmdFSET` functions):
  * **Functionality:** Handles write operations to object properties.
  * **Code Impact (`cmdSET` / `cmdFSET`):**
    1. Added a parser for the `NEWER <field>` trailing argument. If present, it attaches `newerField` to the operational context.
    2. Before persisting the mutation to internal B-Trees (`col.Set(obj)`), it checks if the old object exists.
    3. It grabs the existing double/float equivalent (`oldVal.Value().Num()`) for the specified field.
    4. It compares it to the incoming request's value (`newVal`).
    5. **If the incoming value is `newVal <= oldVal`**, the operational flow is aborted gracefully (the object is NOT updated and the geofence engine is bypassed entirely). It returns a fake success variable in JSON (`{"ok": true, "caught_up": false}`) or RESP integer (`0`) to instruct clients that the signal was received but excluded chronologically. 
    6. Returns normal success if `newVal > oldVal`.

* **`tests/fence_test.go`**:
  * **Functionality:** Added two robust end-to-end integration tests (`fence_speedlimit_test` and `fence_newer_test`).
  * **Coverage:** Simulates multiple vehicle state changes (e.g., accelerating past bounds, out-of-order Redis `FSET` instructions returning integer counts of exactly zero modifications).

### Type Handling Notes (`field.Value`)
When manipulating fields dynamically in Tile38, it is fundamental to use the internal `field.Value().Num()` extraction hook instead of standard casting, since numbers, booleans, and abstract schemas are grouped under custom `field` structures that safely evaluate types internally without crashing.
