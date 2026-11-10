import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { GitFork } from "@/components/app-icons";
import { createContext, useCallback, useContext, useRef } from "react";
import { useTranslation } from "react-i18next";
import { apiErrorException } from "../lib/api-error-exception";
import { clientForSessionHost } from "../lib/host-clients";
import { sessionNavigateTarget } from "../lib/navigate-to-session";
import { cn } from "../lib/utils";
import { useUiStore } from "../stores/ui-store";
import type { WorkspaceSession } from "../types/workspace";
import { Button } from "./ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

// Session fork. One
// click forks the session at its latest finished turn and opens the fork.

/** The session a chat timeline belongs to, so its turn rows can offer Fork. */
export const ForkSessionContext = createContext<WorkspaceSession | null>(null);
/** A finished turn's group: Fork copies the session as that turn left it. */
export type ForkableTurn = { turnId: string; latest: boolean };
export const ForkableTurnContext = createContext<ForkableTurn | null>(null);

/** Whether a session can be forked, where it came from, and its forks. */
export function useSessionForkOptions(session: WorkspaceSession | undefined, enabled = true) {
	const { t } = useTranslation();
	const client = clientForSessionHost(session?.hostId);
	return useQuery({
		enabled: enabled && Boolean(session),
		// Status and setup state are in the key: a busy or still-setting-up
		// session becomes forkable when it goes idle or its worktree is ready.
		queryKey: ["session-fork-options", session?.hostId ?? "", session?.id ?? "", session?.status, session?.provisionState ?? ""],
		queryFn: async () => {
			const { data, error } = await client.GET("/api/v1/sessions/{sessionId}/fork-options", {
				params: { path: { sessionId: session?.id ?? "" } },
			});
			if (error || !data) throw apiErrorException(error, t("sessionFork.unavailable"));
			return data;
		},
	});
}

function useSessionFork(session: WorkspaceSession, turnId?: string) {
	const { t } = useTranslation();
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const client = clientForSessionHost(session.hostId);
	// One id per click, so a retried request returns the fork the first made.
	const requestId = useRef<string | null>(null);
	// Blocks a second click that lands before the pending state renders.
	const inFlight = useRef(false);
	const options = useSessionForkOptions(session);
	const fork = useMutation({
		mutationFn: async () => {
			requestId.current ??= crypto.randomUUID();
			const { data, error } = await client.POST("/api/v1/sessions/{sessionId}/fork", {
				params: { path: { sessionId: session.id } },
				body: { clientRequestId: requestId.current, ...(turnId ? { turnId } : {}) },
			});
			if (error || !data) throw apiErrorException(error, t("sessionFork.failed"));
			return data;
		},
		onSuccess: (data) => {
			requestId.current = null;
			void queryClient.invalidateQueries({ queryKey: ["session-fork-options"] });
			void navigate(sessionNavigateTarget(session.workspaceId, data.sessionId, session.hostId));
		},
	});
	const refusal = options.data?.refusal?.message;
	const unavailable = !options.data?.allowed || fork.isPending;
	const tooltip = refusal ?? (options.error ? options.error.message : fork.isPending ? t("sessionFork.forking") : t("sessionFork.tooltip"));
	const start = () => {
		if (unavailable || inFlight.current) return;
		inFlight.current = true;
		fork.mutate(undefined, { onSettled: () => { inFlight.current = false; } });
	};
	return { refusal, unavailable, tooltip, start, pending: fork.isPending, error: fork.error, anyTurn: options.data?.anyTurn === true };
}

/**
 * Fork beside Copy and Roll back on the latest finished turn of a chat. It
 * renders nothing on any other turn.
 */
export function TurnForkButton() {
	const session = useContext(ForkSessionContext);
	const turn = useContext(ForkableTurnContext);
	if (!session || !turn) return null;
	return <TurnForkButtonInner session={session} turn={turn} />;
}

function TurnForkButtonInner({ session, turn }: { session: WorkspaceSession; turn: ForkableTurn }) {
	const { t } = useTranslation();
	const fork = useSessionFork(session, turn.turnId);
	// Older turns fork only where the agent can cut its copy at a turn.
	if (!turn.latest && !fork.anyTurn) return null;
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<Button
					type="button"
					variant="quiet"
					size="icon-sm"
					onClick={fork.start}
					aria-disabled={fork.unavailable}
					aria-label={t("sessionFork.fork")}
					title={fork.refusal}
					data-testid="fork-turn"
					className={cn(fork.unavailable && "opacity-40")}
				>
					<GitFork aria-hidden="true" className={cn("size-3.5", fork.pending && "animate-pulse motion-reduce:animate-none")} />
				</Button>
			</TooltipTrigger>
			<TooltipContent side="bottom">{fork.error ? fork.error.message : fork.tooltip}</TooltipContent>
		</Tooltip>
	);
}

/**
 * Forks a session at its latest finished turn from a menu, and opens the
 * fork. The menu closes as the item is picked, so this runs independently of
 * any mounted component; a refusal or failure shows as a toast.
 */
export function useForkSessionFromMenu() {
	const { t } = useTranslation();
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	return useCallback(
		(session: WorkspaceSession) => {
			const client = clientForSessionHost(session.hostId);
			void (async () => {
				const { data, error } = await client.POST("/api/v1/sessions/{sessionId}/fork", {
					params: { path: { sessionId: session.id } },
					body: { clientRequestId: crypto.randomUUID() },
				});
				if (error || !data) {
					showGlobalToast(t("sessionFork.failed"), apiErrorException(error, t("sessionFork.failed")).message, "error");
					return;
				}
				void queryClient.invalidateQueries({ queryKey: ["session-fork-options"] });
				void navigate(sessionNavigateTarget(session.workspaceId, data.sessionId, session.hostId));
			})();
		},
		[navigate, queryClient, showGlobalToast, t],
	);
}

/**
 * In a fork whose agent was given its original's conversation as text, and
 * which has no messages yet, says so above the composer.
 */
export function ForkTextCopyNotice() {
	const session = useContext(ForkSessionContext);
	if (!session) return null;
	return <ForkTextCopyNoticeInner session={session} />;
}

function ForkTextCopyNoticeInner({ session }: { session: WorkspaceSession }) {
	const { t } = useTranslation();
	const origin = useSessionForkOptions(session).data?.forkedFrom;
	if (!origin?.textHandoff) return null;
	return (
		<p className="mx-auto mb-4 max-w-md px-4 text-center text-sm text-muted-foreground" data-testid="fork-text-copy-notice">
			{t("sessionFork.textCopyNotice", { title: origin.title })}
		</p>
	);
}
