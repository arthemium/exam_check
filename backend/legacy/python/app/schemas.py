from datetime import datetime
from typing import Literal

from pydantic import BaseModel, ConfigDict, Field, field_validator, model_validator

CheckType = Literal["grammar", "spelling", "punctuation", "structure"]


class InputModel(BaseModel):
    model_config = ConfigDict(str_strip_whitespace=True, extra="forbid")


class FolderInput(InputModel):
    name: str = Field(min_length=1, max_length=120)


class FolderOutput(BaseModel):
    id: str
    name: str
    exam_count: int
    created_at: datetime


class ExamInput(InputModel):
    title: str = Field(min_length=1, max_length=120)
    student_name: str = Field(default="", max_length=120)
    checks: list[CheckType] = Field(default_factory=list, max_length=4)

    @field_validator("checks")
    @classmethod
    def unique_checks(cls, value):
        return list(dict.fromkeys(value))


class ExamPatch(InputModel):
    title: str | None = Field(default=None, min_length=1, max_length=120)
    student_name: str | None = Field(default=None, max_length=120)
    checks: list[CheckType] | None = Field(default=None, max_length=4)

    @model_validator(mode="after")
    def reject_null_changes(self):
        if any(getattr(self, name) is None for name in self.model_fields_set):
            raise ValueError("Use an empty student name or checks list instead of null")
        return self


class ExamOutput(BaseModel):
    model_config = ConfigDict(from_attributes=True)
    id: str
    folder_id: str
    title: str
    student_name: str
    checks: list[str]
    status: str
    created_at: datetime


class PageOutput(BaseModel):
    id: str
    page_number: int
    url: str


class ExamDetail(ExamOutput):
    pages: list[PageOutput]
    result: dict | None
    error: str | None


class JobOutput(BaseModel):
    model_config = ConfigDict(from_attributes=True)
    id: str
    exam_id: str
    status: str
