import { fireEvent, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import { defaultCriteria } from '../criteria';
import type { Exam } from '../types';
import { PhotoViewer } from './PhotoViewer';

const exam: Exam = {
  id: 'exam', folderId: 'folder', title: 'Essay', studentName: 'Anna',
  source: 'upload', status: 'completed', criteria: defaultCriteria(),
  photos: [
    { id: 'first', url: '/first.jpg', fileName: 'first.jpg', pageNumber: 1 },
    { id: 'second', url: '/second.jpg', fileName: 'second.jpg', pageNumber: 2 },
  ],
};

describe('PhotoViewer', () => {
  it('enlarges the selected page, bounds zoom, and fits the photo again', async () => {
    const user = userEvent.setup();
    render(<PhotoViewer exam={exam} photos={exam.photos} compact />);
    await user.selectOptions(screen.getByRole('combobox', { name: 'Photo page' }), '1');
    await user.click(screen.getByRole('button', { name: 'Enlarge photo' }));
    const dialog = within(screen.getByRole('dialog', { name: 'Original exam photo' }));
    expect(dialog.getByRole('img')).toHaveAttribute('src', '/second.jpg');
    expect(dialog.getByRole('button', { name: 'Zoom out' })).toBeDisabled();
    for (let step = 0; step < 4; step++) await user.click(dialog.getByRole('button', { name: 'Zoom in' }));
    expect(dialog.getByLabelText('Photo zoom')).toHaveTextContent('300%');
    expect(dialog.getByRole('img').parentElement).toHaveStyle({ width: '300%' });
    expect(dialog.getByRole('button', { name: 'Zoom in' })).toBeDisabled();
    await user.click(dialog.getByRole('button', { name: 'Zoom out' }));
    expect(dialog.getByLabelText('Photo zoom')).toHaveTextContent('250%');
    await user.click(dialog.getByRole('button', { name: 'Fit photo' }));
    expect(dialog.getByLabelText('Photo zoom')).toHaveTextContent('100%');
    expect(dialog.getByRole('img').parentElement).toHaveStyle({ width: '100%' });
  });

  it('keeps page changes after closing, resets zoom, and handles Escape cancellation', async () => {
    const user = userEvent.setup();
    render(<PhotoViewer exam={exam} photos={exam.photos} />);
    const enlarge = screen.getByRole('button', { name: 'Enlarge photo' });
    await user.click(enlarge);
    const dialog = within(screen.getByRole('dialog'));
    await user.selectOptions(dialog.getByRole('combobox', { name: 'Photo page' }), '1');
    await user.click(dialog.getByRole('button', { name: 'Zoom in' }));
    await user.click(dialog.getByRole('button', { name: 'Close dialog' }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(screen.getByRole('img')).toHaveAttribute('src', '/second.jpg');
    expect(enlarge).toHaveFocus();
    await user.click(enlarge);
    expect(within(screen.getByRole('dialog')).getByLabelText('Photo zoom')).toHaveTextContent('100%');
    fireEvent(screen.getByRole('dialog'), new Event('cancel', { bubbles: false, cancelable: true }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(enlarge).toHaveFocus();
  });

  it('disables enlargement when an uploaded exam has no photo', () => {
    render(<PhotoViewer exam={{ ...exam, photos: [] }} photos={[]} />);
    expect(screen.getByText('No photo preview is available.')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Enlarge photo' })).toBeDisabled();
  });
});
