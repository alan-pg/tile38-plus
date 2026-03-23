# Tile38-Plus Exclusive Features

Tile38-Plus includes a set of advanced features built directly into its core to tackle complexities found in fleet tracking, vehicle behavior analysis, and message ordering.

---

## 1. Speed Limit Geofences (`SPEEDLIMIT`)

Native Tile38 handles spatial transitions (like `enter`, `exit`, `inside`), but often we need to know what anomalous behaviors are happening inside those zones. We've extended the `FENCE` command payload to natively parse **Speed Limits** and fire specific webhooks when vehicles break these limits.

### Syntax
When setting up a fence, you can now append `SPEEDLIMIT <limit> <speed_field>` to your detect constraints.

```bash
# Example
SETCHAN school_zone WITHIN fleet FENCE DETECT inside,overspeed,underspeed SPEEDLIMIT 40.0 speed BOUNDS -90 -180 90 180
```

### Emitted Events (`detect` values)
When receiving webhook/pubsub messages, your application can now expect:
* `"detect": "overspeed"` - Fired when the spatial point is `inside` the fence AND the vehicle's speed has just transitioned from *below or equal to* the speed limit to *greater than* the speed limit.
* `"detect": "underspeed"` - Fired when the point is `inside` the fence AND the vehicle's speed has just dropped from *above* the speed limit to *less than or equal to* the speed limit.

### Example Flow
1. **Speed = 30** (limit is 40): Nothing happens (or normal `inside` event if monitored).
2. **Speed = 50**: Fires `overspeed`.
3. **Speed = 55**: No additional `overspeed` emitted (already flagged as overspeed), only normal `inside` updates.
4. **Speed = 35**: Fires `underspeed`.

### Global Speed Limits (No Boundaries)
If you want to monitor speed limits globally without a specific geographic boundary, you can create a geofence covering the entire world using `BOUNDS -90 -180 90 180`:

```bash
# Example: Global limit of 120 km/h for the entire 'fleet' collection
SETCHAN global_limit WITHIN fleet FENCE DETECT overspeed,underspeed SPEEDLIMIT 120.0 speed BOUNDS -90 -180 90 180
```

---

## 2. Chronological Out-of-Order Rejection (`NEWER`)

GPS devices natively batch signals when entering tunnels or areas with poor coverage. These are often sent out-of-order over MQTT or HTTP endpoints compared to more recent data. By default, Tile38 blindly overwrites coordinate data with whatever arrives last at its TCP socket. 
With `NEWER`, Tile38 filters out old data natively.

### Syntax
You can pass the keyword `NEWER <field_name>` to the `SET` and `FSET` commands.

```bash
# Example
SET fleet truck1 FIELD timestamp 1700001000 NEWER timestamp POINT 10 10
```

### Mechanism
When the `SET` command arrives:
1. It reads the specific integer/float value inside the existing `<field_name>` for the object.
2. It compares it to the incoming request's value for the identical `<field_name>`.
3. **If the incoming value is NOT strictly greater than the existing value:**
   * The entire update is silently discarded.
   * Fences are **not** triggered.
   * `SET` returns an integer `0` (or `{"caught_up": false}` inside JSON output schemas) to safely swallow the request.
4. **If the incoming value IS greater:**
   * The coordinate and fields update natively, triggering fences normally.
