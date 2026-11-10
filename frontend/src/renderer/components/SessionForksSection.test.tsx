import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "../stores/ui-store";
import type { WorkspaceSession } from "../types/workspace";
import { SessionForksSection } from "./SessionForksSection";

const { getMock, navigateMock } = vi.hoisted(() => ({ getMock: vi.fn(), navigateMock: vi.fn() }));

vi.mock("../lib/host-clients", async (importOriginal) => ({
	...(await importOriginal<typeof import("../lib/host-clients")>()),
	clientForSessionHost: () => ({ GET: getMock }),
}));
vi.mock("@tanstack/react-router", async (importOriginal) => ({
	...(await importOriginal<typeof import("@tanstack/react-router")>()),
	useNavigate: () => navigateMock,
}));

const session = { id: "p-9", workspaceId: "p", title: "Fork of Fix login", status: "idle", mode: "chat" } as unknown as WorkspaceSession;

function renderSection() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<SessionForksSection session={session} />
		</QueryClientProvider>,
	);
}

describe("SessionForksSection", () => {
	beforeEach(() => useUiStore.setState({ developerMode: true }));
	afterEach(() => {
		vi.clearAllMocks();
		useUiStore.setState({ developerMode: false });
	});

	it("links a fork to its original and shows the original's PRs read-only", async () => {
		getMock.mockResolvedValue({ data: {
			allowed: true,
			forkedFrom: { sessionId: "p-1", title: "Fix login", sourceExists: true },
			sourcePRs: [{ url: "https://github.com/a/b/pull/7", number: 7, merged: false, closed: false }],
		} });
		renderSection();
		fireEvent.click(await screen.findByRole("button", { name: "Forked from Fix login" }));
		expect(JSON.stringify(navigateMock.mock.calls[0][0])).toContain("p-1");
		expect(screen.getByRole("link", { name: "Original's PR #7" })).toHaveAttribute("href", "https://github.com/a/b/pull/7");
	});

	it("marks a fork whose agent was given its conversation as text", async () => {
		getMock.mockResolvedValue({ data: { allowed: true, forkedFrom: { sessionId: "p-1", title: "Fix login", sourceExists: true, textHandoff: true } } });
		renderSection();
		expect(await screen.findByText("Conversation copied as text")).toBeInTheDocument();
	});

	it("lists the forks of an original", async () => {
		getMock.mockResolvedValue({ data: { allowed: true, forks: [
			{ sessionId: "p-10", title: "Fork of Fix login", exists: true },
			{ sessionId: "p-11", title: "Fork 2 of Fix login", exists: false },
		] } });
		renderSection();
		fireEvent.click(await screen.findByRole("button", { name: "Fork of Fix login" }));
		expect(JSON.stringify(navigateMock.mock.calls[0][0])).toContain("p-10");
		expect(screen.getByText("Fork 2 of Fix login")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Fork 2 of Fix login" })).toBeNull();
	});

	it("renders nothing for a session that is neither a fork nor forked", async () => {
		getMock.mockResolvedValue({ data: { allowed: true } });
		const { container } = renderSection();
		await waitFor(() => expect(getMock).toHaveBeenCalled());
		expect(container.querySelector("[data-testid=session-forks]")).toBeNull();
	});
});
