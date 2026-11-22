import { render, screen, waitForElementToBeRemoved, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import type { Issue } from '../types';
import { TranscriptHighlight } from './TranscriptHighlight';

const grammar: Issue = { id: 'grammar', category: 'grammar', scope: 'span', paragraphId: 'p', quote: 'practice', start: 10, end: 18, suggestion: 'practices', explanation: 'Use singular subject agreement.' };

describe('TranscriptHighlight', () => {
  it('shows overlapping suggestions on hover and keeps the bubble open while reading it', async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    const style: Issue = { ...grammar, id: 'style', category: 'style', suggestion: 'rehearses', explanation: 'Use a more precise verb.' };
    render(<p>My sister <TranscriptHighlight text="practice" issues={[grammar, style]} selectedId="" onSelect={onSelect} /> daily.</p>);
    const highlight = screen.getByRole('button');
    expect(screen.queryByRole('tooltip')).not.toBeInTheDocument();

    await user.hover(highlight);
    const bubble = screen.getByRole('tooltip');
    expect(within(bubble).getByText(grammar.suggestion)).toBeInTheDocument();
    expect(within(bubble).getByText(grammar.explanation)).toBeInTheDocument();
    expect(within(bubble).getByText(style.suggestion)).toBeInTheDocument();
    expect(highlight.closest('p')?.textContent).toBe('My sister practice daily.');
    expect(highlight).toHaveAttribute('aria-describedby', bubble.id);
    expect(onSelect).not.toHaveBeenCalled();

    await user.hover(bubble);
    await new Promise(resolve => setTimeout(resolve, 180));
    expect(bubble).toBeInTheDocument();
    await user.unhover(bubble);
    await waitForElementToBeRemoved(bubble);
  });

  it('opens with keyboard focus and dismisses with Escape or blur', async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    render(<><TranscriptHighlight text="practice" issues={[grammar]} selectedId="" onSelect={onSelect} /><button>Next</button></>);
    await user.tab();
    expect(screen.getByRole('tooltip')).toHaveTextContent(grammar.explanation);
    await user.hover(screen.getByRole('button', { name: /Grammar feedback/ }));
    await user.unhover(screen.getByRole('button', { name: /Grammar feedback/ }));
    await new Promise(resolve => setTimeout(resolve, 180));
    expect(screen.getByRole('tooltip')).toBeInTheDocument();
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('tooltip')).not.toBeInTheDocument();
    await user.keyboard('{Enter}');
    expect(onSelect).toHaveBeenCalledWith(grammar.id);
    const bubble = screen.getByRole('tooltip');
    await user.tab();
    await waitForElementToBeRemoved(bubble);
  });

  it('opens on touch and closes when tapping outside', async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    render(<><TranscriptHighlight text="practice" issues={[grammar]} selectedId="" onSelect={onSelect} /><button>Outside</button></>);
    await user.pointer([{ keys: '[TouchA>]', target: screen.getByRole('button', { name: /Grammar feedback/ }) }, { keys: '[/TouchA]' }]);
    expect(screen.getByRole('tooltip')).toHaveTextContent(grammar.suggestion);
    expect(onSelect).toHaveBeenCalledWith(grammar.id);
    await user.pointer([{ keys: '[TouchA>]', target: screen.getByRole('button', { name: 'Outside' }) }, { keys: '[/TouchA]' }]);
    expect(screen.queryByRole('tooltip')).not.toBeInTheDocument();
  });
});
