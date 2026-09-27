import { createContext, type ComponentChildren } from "preact";
import { useCallback, useContext, useEffect, useState } from "preact/hooks";
import { AccessLevel, api, errorMessage, GetMeResponseSchema, ListTagsResponseSchema, type Tag, type User } from "./api";
import { cached, isNetworkError } from "./offline";
import { refreshRegistration } from "./notifications";
import { browserTimeZone, today } from "./dates";

export interface Session {
  me: User;
  tags: Tag[];
  tagsById: Map<string, Tag>;
  reloadTags(): Promise<void>;
  setMe(u: User): void;
  /** Today in the user's time zone. */
  today(): string;
}

const SessionContext = createContext<Session | null>(null);

export function useSession(): Session {
  const s = useContext(SessionContext);
  if (!s) throw new Error("no session");
  return s;
}

/** Tags the user can put on tasks (full access). */
export function assignableTags(s: Session): Tag[] {
  return s.tags.filter((t) => t.myAccess === AccessLevel.FULL);
}

export function SessionProvider({ children }: { children: ComponentChildren }) {
  const [me, setMe] = useState<User | null>(null);
  const [tags, setTags] = useState<Tag[]>([]);
  const [error, setError] = useState("");

  const reloadTags = useCallback(async () => {
    setTags((await cached("tags", ListTagsResponseSchema, () => api.listTags({}))).tags);
  }, []);

  useEffect(() => {
    (async () => {
      try {
        let user = (await cached("me", GetMeResponseSchema, () => api.getMe({}))).user!;
        if (!user.timeZone) {
          try {
            user = (await api.updateMe({ timeZone: browserTimeZone() })).user!;
          } catch (err) {
            if (!isNetworkError(err)) throw err;
          }
        }
        setMe(user);
        refreshRegistration();
        await reloadTags();
      } catch (err) {
        setError(errorMessage(err));
      }
    })();
  }, [reloadTags]);

  if (error) return <div class="page"><p class="error">{error}</p></div>;
  if (!me) return <div class="page"><p class="muted">Loading…</p></div>;

  const session: Session = {
    me,
    tags,
    tagsById: new Map(tags.map((t) => [t.id, t])),
    reloadTags,
    setMe,
    today: () => today(me.timeZone),
  };
  return <SessionContext.Provider value={session}>{children}</SessionContext.Provider>;
}
