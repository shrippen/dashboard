"""Widget config forms generated from the pydantic schema.

    class RssConfig(BaseModel): url: str; limit: int = 8
      → [Field("cfg.url", TEXT), Field("cfg.limit", NUMBER, value=8)]
    form data {"cfg.url": ..., "cfg.limit": "8"} → {"url": ..., "limit": "8"} (pydantic converts)
"""

import types
import typing
from dataclasses import dataclass, field
from enum import Enum, StrEnum

from pydantic import BaseModel

PREFIX = "cfg."
LIST_SEP = ","


class Input(StrEnum):
    TEXT = "text"
    NUMBER = "number"
    CHECK = "checkbox"
    SELECT = "select"
    LIST = "list"
    AREA = "textarea"


LONG_TEXT = {"text", "description"}


@dataclass
class FormField:
    name: str
    key: str
    input: Input
    value: object = None
    options: list[str] = field(default_factory=list)
    required: bool = False
    step: str = "any"


def _unwrap(annotation):
    """Optional[X] → (X, optional)."""
    origin = typing.get_origin(annotation)
    if origin in (typing.Union, types.UnionType):
        args = [a for a in typing.get_args(annotation) if a is not type(None)]
        if len(args) == 1:
            return args[0], True
    return annotation, False


def fields(schema: type[BaseModel], values: dict | None, prefix: str = PREFIX) -> list[FormField]:
    values = values or {}
    result = []
    for name, info in schema.model_fields.items():
        annotation, optional = _unwrap(info.annotation)
        current = values.get(name, None if info.is_required() else info.get_default(call_default_factory=True))
        key = name
        full = prefix + name

        if isinstance(annotation, type) and issubclass(annotation, BaseModel):
            result += fields(annotation, current if isinstance(current, dict) else {}, full + ".")
            continue

        origin = typing.get_origin(annotation)
        if origin is list:
            text = LIST_SEP.join(str(v) for v in current or [])
            result.append(FormField(full, key, Input.LIST, text))
        elif annotation is bool:
            result.append(FormField(full, key, Input.CHECK, bool(current)))
        elif isinstance(annotation, type) and issubclass(annotation, Enum):
            value = current.value if isinstance(current, Enum) else current
            result.append(FormField(full, key, Input.SELECT, value, [m.value for m in annotation]))
        elif annotation in (int, float):
            step = "1" if annotation is int else "any"
            result.append(FormField(full, key, Input.NUMBER, current, required=info.is_required(), step=step))
        else:
            kind = Input.AREA if name in LONG_TEXT else Input.TEXT
            result.append(FormField(full, key, kind, current or "", required=info.is_required() and not optional))
    return result


def parse(schema: type[BaseModel], form: dict, prefix: str = PREFIX) -> dict:
    """Form data → dict for schema validation."""
    result: dict = {}
    for name, info in schema.model_fields.items():
        annotation, optional = _unwrap(info.annotation)
        full = prefix + name

        if isinstance(annotation, type) and issubclass(annotation, BaseModel):
            nested = parse(annotation, form, full + ".")
            if optional and not any(v not in ("", None, [], False) for v in nested.values()):
                result[name] = None
            else:
                result[name] = nested
            continue

        raw = form.get(full)
        origin = typing.get_origin(annotation)
        if origin is list:
            parts = [p.strip() for p in str(raw or "").replace("\n", LIST_SEP).split(LIST_SEP)]
            result[name] = [p for p in parts if p]
        elif annotation is bool:
            result[name] = raw is not None
        elif raw in (None, "") and not info.is_required():
            continue
        else:
            result[name] = raw
    return result
