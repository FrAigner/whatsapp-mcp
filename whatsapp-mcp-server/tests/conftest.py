import pytest


@pytest.fixture(autouse=True)
def _approval_off_by_default(monkeypatch):
    """Legacy tests use unfiltered fixtures; test_contact_approval.py turns filtering back on."""
    monkeypatch.setenv("WHATSAPP_REQUIRE_APPROVAL", "false")
