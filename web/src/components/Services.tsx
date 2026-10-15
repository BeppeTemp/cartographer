import { useEffect, useState } from "react";
import { fetchServices } from "../api/client";
import type { ServiceEntry } from "../api/types";
import { Count, Page, PageHeader } from "./Page";
import { EmptyState, ErrorState, Skeleton } from "./States";

/**
 * The Services panel: the concepts of type Service, the things the KB's
 * documentation says are running. Filtered by what the viewer may see.
 */
export function Services({
  kb,
  reloadKey,
  onOpenConcept,
  onRetry,
  onFailure,
}: {
  kb: string;
  reloadKey: string;
  onOpenConcept?(id: string): void;
  onRetry(): void;
  onFailure(err: unknown): boolean;
}) {
  const [services, setServices] = useState<ServiceEntry[] | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    setServices(null);
    setLoading(true);
  }, [kb]);

  useEffect(() => {
    const controller = new AbortController();
    fetchServices(kb, controller.signal)
      .then((data) => {
        setServices(data.services ?? []);
        setError(null);
      })
      .catch((err: unknown) => {
        if (controller.signal.aborted || onFailure(err)) return;
        setError(err);
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [kb, reloadKey]);

  if (loading && !services) return <Skeleton lines={6} label="Loading services" />;
  if (error) return <ErrorState error={error} onRetry={onRetry} />;
  if (!services) return null;
  if (services.length === 0) {
    return (
      <EmptyState
        title="No services documented yet"
        detail="A Service is a concept of type Service: one running thing (an API, a database, a job) with where it lives and who depends on it. An agent records one with concept_write or concept_new using that type, and service_list then lists them all."
      />
    );
  }
  return (
    <Page label="Services" className="ref-page">
      <PageHeader
        eyebrow="Services"
        title={
          <>
            <Count>{services.length}</Count> service{services.length === 1 ? "" : "s"} documented.
          </>
        }
        subtitle="The concepts of type Service you can see, by id."
      />
      <ul className="ref-list">
        {services.map((s) => (
          <li key={s.id} className="ref-row">
            <div className="ref-row__head">
              {onOpenConcept ? (
                <button type="button" className="ref-link ref-row__title" onClick={() => onOpenConcept(s.id)}>
                  {s.title || s.id}
                </button>
              ) : (
                <span className="ref-row__title">{s.title || s.id}</span>
              )}
              <code className="ref-row__name">{s.id}</code>
              {s.status && (
                <span className="pill" data-status={s.status}>
                  {s.status}
                </span>
              )}
            </div>
          </li>
        ))}
      </ul>
    </Page>
  );
}
