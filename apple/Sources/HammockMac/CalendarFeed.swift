import AppKit
import EventKit
import HammockKit

/// Calendar triggers (the daemon's schedcalendar.go): while a scheduled task
/// on this Mac has one, the app reads your calendars (EventKit, with your
/// leave) and sends the daemon the events of the next few days, again as
/// they change. The daemon keeps them, so runs still start with the app
/// closed, until what it was sent runs out. Only this Mac's own daemon:
/// the daemon is Go and can't ask for calendar access itself, and a helper
/// would need a permission of its own.
@MainActor
final class CalendarFeed {
    static let shared = CalendarFeed()

    /// How far ahead events are sent, and how often they're sent again
    /// (besides when the calendars change).
    private static let horizon: TimeInterval = 7 * 24 * 3600
    private static let every: TimeInterval = 30 * 60

    private let store = EKEventStore()
    private weak var model: AppModel?
    private var timer: Timer?
    private var lastSent: Hammock_V1_CalendarEvents?
    private var lastSentAt = Date.distantPast
    private var observing = false

    /// Starts following the model's schedules (once the daemon's state is in).
    func attach(_ model: AppModel) {
        self.model = model
        if timer == nil {
            timer = Timer.scheduledTimer(withTimeInterval: 60, repeats: true) { _ in
                Task { @MainActor in CalendarFeed.shared.refresh() }
            }
        }
        refresh()
    }

    /// Whether any of this Mac's tasks has a calendar trigger.
    private var wanted: Bool {
        guard let model else { return false }
        return model.schedules.values.contains { s in
            (s.computerID.isEmpty || s.computerID == model.myUserID) && s.events.contains { if case .calendar = $0.kind { true } else { false } }
        }
    }

    /// Asks for calendar access (a calendar trigger was just added), then sends.
    func requestAccess() {
        guard EKEventStore.authorizationStatus(for: .event) == .notDetermined else { refresh(force: true); return }
        Task { @MainActor in
            _ = try? await store.requestFullAccessToEvents()
            refresh(force: true)
        }
    }

    /// Sends the daemon what's coming up, if any task wants it and it's
    /// changed (or it's been a while).
    func refresh(force: Bool = false) {
        guard let model, model.connected, wanted else { return }
        if !observing {
            observing = true
            NotificationCenter.default.addObserver(forName: .EKEventStoreChanged, object: store, queue: .main) { _ in
                Task { @MainActor in CalendarFeed.shared.refresh(force: true) }
            }
        }
        var msg = Hammock_V1_CalendarEvents()
        switch EKEventStore.authorizationStatus(for: .event) {
        case .fullAccess:
            let now = Date()
            let until = now.addingTimeInterval(Self.horizon)
            msg.throughUnixMs = Int64(until.timeIntervalSince1970 * 1000)
            let pred = store.predicateForEvents(withStart: now.addingTimeInterval(-3600), end: until, calendars: nil)
            msg.events = store.events(matching: pred).filter { !$0.isAllDay }.prefix(2000).map(Self.event)
        case .notDetermined:
            requestAccess()
            return
        default:
            msg.denied = true
        }
        // The same as last time, and not long ago: nothing to say.
        var same = msg
        same.throughUnixMs = lastSent?.throughUnixMs ?? 0
        if !force, same == lastSent, Date().timeIntervalSince(lastSentAt) < Self.every { return }
        lastSent = msg
        lastSentAt = Date()
        Task { try? await model.sendCalendarEvents(msg) }
    }

    private static func event(_ e: EKEvent) -> Hammock_V1_CalendarEvent {
        var out = Hammock_V1_CalendarEvent()
        let start = e.startDate ?? Date()
        // Recurring events share an identifier: each occurrence is its start.
        out.id = (e.eventIdentifier ?? e.calendarItemIdentifier) + "@" + String(Int64(start.timeIntervalSince1970))
        out.title = e.title ?? ""
        out.calendar = e.calendar?.title ?? ""
        out.startUnixMs = Int64(start.timeIntervalSince1970 * 1000)
        out.endUnixMs = Int64((e.endDate ?? start).timeIntervalSince1970 * 1000)
        out.attendees = Int32((e.attendees ?? []).filter { !$0.isCurrentUser }.count)
        out.location = e.location ?? ""
        out.url = e.url?.absoluteString ?? ""
        out.notes = String((e.notes ?? "").prefix(2000))
        return out
    }
}
