"""Dawarich metrics: client visits, travel distance, time away from home."""

import math
from collections import defaultdict
from datetime import date

from app.metrics.dates import parse_day, parse_time

EARTH_KM = 6371.0
ROAD_FACTOR = 1.3


def distance_km(a: tuple[float, float], b: tuple[float, float]) -> float:
    """Great-circle distance (haversine)."""
    lat1, lon1, lat2, lon2 = map(math.radians, (*a, *b))
    h = math.sin((lat2 - lat1) / 2) ** 2 + math.cos(lat1) * math.cos(lat2) * math.sin((lon2 - lon1) / 2) ** 2
    return 2 * EARTH_KM * math.asin(math.sqrt(h))


def _area_of(visit: dict, areas: list[dict]) -> dict | None:
    if visit.get("area_id") is not None:
        for area in areas:
            if area["id"] == visit["area_id"]:
                return area
    if visit.get("lat") is None:
        return None
    for area in areas:
        if distance_km((visit["lat"], visit["lon"]), (area["lat"], area["lon"])) * 1000 <= area["radius"] + 50:
            return area
    return None


def client_visits(data: dict, mapping: dict) -> list[dict]:
    """Visits in areas mapped to a Kimai customer.

    mapping = {"Muster GmbH Büro": {"customer_id": 1}, "Home": {"home": true}}
    """
    areas = data.get("areas", [])
    result = []
    for visit in data.get("visits", []):
        area = _area_of(visit, areas)
        if area is None:
            continue
        target = mapping.get(area["name"]) or {}
        if not target.get("customer_id"):
            continue
        start, end = parse_time(visit.get("start")), parse_time(visit.get("end"))
        minutes = visit.get("minutes") or (int((end - start).total_seconds() // 60) if start and end else 0)
        result.append({"day": parse_day(visit.get("start")), "customer_id": target["customer_id"],
                       "area": area, "minutes": minutes, "start": start, "end": end})
    return result


def home(data: dict, mapping: dict) -> dict | None:
    for area in data.get("areas", []):
        if (mapping.get(area["name"]) or {}).get("home"):
            return area
    return None


def trips(data: dict, mapping: dict, start: date, end: date) -> list[dict]:
    """One round trip per client day: straight line × road factor, there and back."""
    base = home(data, mapping)
    per_day: dict = defaultdict(list)
    for visit in client_visits(data, mapping):
        if start <= visit["day"] <= end:
            per_day[visit["day"]].append(visit)

    result = []
    for day, visits in sorted(per_day.items()):
        area = visits[0]["area"]
        km = 0.0
        if base:
            km = 2 * distance_km((base["lat"], base["lon"]), (area["lat"], area["lon"])) * ROAD_FACTOR
        first = min(v["start"] for v in visits if v["start"]) if any(v["start"] for v in visits) else None
        last = max(v["end"] for v in visits if v["end"]) if any(v["end"] for v in visits) else None
        away = int((last - first).total_seconds() // 60) if first and last else sum(v["minutes"] for v in visits)
        result.append({"day": day.isoformat(), "customer_id": visits[0]["customer_id"], "km": round(km, 1),
                       "away_min": away, "area": area["name"]})
    return result
